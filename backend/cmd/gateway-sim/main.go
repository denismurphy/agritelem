// Command gateway-sim plays the part of an IoT edge gateway. It generates
// hourly telemetry for every animal at a location, posts it in batches, and
// reports throughput and latency.
//
// The data is deterministic for a given -seed and includes scripted episodes
// on the last complete UTC day: one animal in estrus, one with an elevated
// reticular temperature, and one whose sensor dropped out for ten hours.
// Source IDs are deterministic too, so re-running the simulator replays
// rather than duplicates.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/httpapi"
)

type options struct {
	baseURL     string
	token       string
	location    domain.Location
	source      string
	days        int
	batchSize   int
	concurrency int
	seed        uint64
}

func main() {
	var o options
	var loc string
	flag.StringVar(&o.baseURL, "url", "http://localhost:8080", "API base URL")
	flag.StringVar(&o.token, "token", os.Getenv("AGRITELEM_TOKEN"), "bearer token (default $AGRITELEM_TOKEN)")
	flag.StringVar(&loc, "location", "dk.herdnumber/123456", "location as scheme/id")
	flag.StringVar(&o.source, "source", "ie.example.gateway", "meta.source; must equal the token subject")
	flag.IntVar(&o.days, "days", 21, "complete UTC days of history to generate")
	flag.IntVar(&o.batchSize, "batch", 100, "resources per request")
	flag.IntVar(&o.concurrency, "concurrency", 4, "concurrent requests")
	flag.Uint64Var(&o.seed, "seed", 1, "random seed")
	flag.Parse()

	scheme, id, ok := strings.Cut(loc, "/")
	switch {
	case !ok:
		fatal(fmt.Errorf("-location %q is not scheme/id", loc))
	case o.token == "":
		fatal(errors.New("a token is required: pass -token or set AGRITELEM_TOKEN"))
	case o.days < 1 || o.batchSize < 1 || o.concurrency < 1:
		fatal(errors.New("-days, -batch and -concurrency must be positive"))
	}
	o.location = domain.Location{Scheme: scheme, ID: id}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, o, time.Now().UTC()); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gateway-sim:", err)
	os.Exit(1)
}

func run(ctx context.Context, o options, now time.Time) error {
	c := &client{base: o.baseURL, token: o.token, http: &http.Client{Timeout: 60 * time.Second}}
	animals, err := c.animals(ctx, o.location)
	if err != nil {
		return err
	}
	if len(animals) == 0 {
		return errors.New("no animals at the location; run `make seed` first")
	}
	resources := generate(animals, o, now)
	stats := 0
	for _, r := range resources {
		stats += len(*r.Statistics)
	}
	fmt.Printf("posting %d resources (%d hourly statistics) for %d animals in batches of %d\n",
		len(resources), stats, len(animals), o.batchSize)

	report, err := c.post(ctx, o, resources)
	if err != nil {
		return err
	}
	report.print(stats)
	return nil
}

// Scenario roles, by position in the location's animal list.
const (
	estrusAnimal = 2
	feverAnimal  = 5
	gapAnimal    = 8
)

func generate(animals []httpapi.IcarAnimalIdentifierType, o options, now time.Time) []httpapi.IcarObservationSummaryResource {
	today := now.Truncate(24 * time.Hour)
	lastDay := today.AddDate(0, 0, -1)
	var out []httpapi.IcarObservationSummaryResource
	for i, a := range animals {
		rng := rand.New(rand.NewPCG(o.seed, uint64(i))) //nolint:gosec // deterministic simulation data, not security
		baseline := herdMember(rng)
		for d := o.days; d >= 1; d-- {
			day := today.AddDate(0, 0, -d)
			var hours []int
			for h := range 24 {
				if day.Equal(lastDay) && i == gapAnimal && h >= 8 && h < 18 {
					continue // sensor dropout: 14 of 24 hours delivered
				}
				hours = append(hours, h)
			}
			episode := normal
			if day.Equal(lastDay) {
				switch i {
				case estrusAnimal:
					episode = estrus
				case feverAnimal:
					episode = fever
				}
			}
			out = append(out, resource(a, o.source, fmt.Sprintf("sim:%s:%s", a.Id, day.Format(time.DateOnly)),
				day, hours, baseline, episode, rng))
		}
		// Today's completed hours, one resource per hour so later runs can
		// add hours without reusing a source ID.
		for h := 0; today.Add(time.Duration(h+1) * time.Hour).Before(now); h++ {
			out = append(out, resource(a, o.source, fmt.Sprintf("sim:%s:%s", a.Id, today.Add(time.Duration(h)*time.Hour).Format(time.RFC3339)),
				today, []int{h}, baseline, normal, rng))
		}
	}
	return out
}

// member holds one animal's typical behaviour.
type member struct {
	rumination, eating, lying, steps, temperature float64 // daily totals; temperature in °C
}

func herdMember(rng *rand.Rand) member {
	return member{
		rumination:  470 + rng.NormFloat64()*25,
		eating:      270 + rng.NormFloat64()*20,
		lying:       690 + rng.NormFloat64()*40,
		steps:       3000 + rng.NormFloat64()*300,
		temperature: 38.9 + rng.NormFloat64()*0.1,
	}
}

type episode int

const (
	normal episode = iota
	estrus         // rumination falls, activity rises
	fever          // reticular temperature rises
)

// diurnal weights an hour's share of a daily total: cows ruminate and lie
// mostly at night and walk and eat mostly by day.
func diurnal(hour int, night bool) float64 {
	phase := math.Cos(2 * math.Pi * float64(hour-3) / 24) // peaks at 03:00
	if !night {
		phase = -phase
	}
	return 1 + 0.5*phase
}

func resource(a httpapi.IcarAnimalIdentifierType, source, sourceID string, day time.Time, hours []int, m member, e episode, rng *rand.Rand) httpapi.IcarObservationSummaryResource {
	rum, steps, temp := m.rumination, m.steps, m.temperature
	switch e {
	case estrus:
		rum *= 0.72
		steps *= 2.4
	case fever:
		temp += 1.3
	}
	jitter := func(v, sd float64) float64 { return v * (1 + rng.NormFloat64()*sd) }

	var stats []httpapi.IcarObservationStatisticsType
	for _, h := range hours {
		start := day.Add(time.Duration(h) * time.Hour)
		stats = append(stats,
			stat(domain.RuminationTime, start, clamp(jitter(rum/24*diurnal(h, true), 0.08), 0, 60)),
			stat(domain.EatingTime, start, clamp(jitter(m.eating/24*diurnal(h, false), 0.1), 0, 60)),
			stat(domain.LyingTime, start, clamp(jitter(m.lying/24*diurnal(h, true), 0.08), 0, 60)),
			stat(domain.StepCount, start, math.Round(clamp(jitter(steps/24*diurnal(h, false), 0.12), 0, 5000))),
			stat(domain.ReticularTemperature, start, math.Round(clamp(temp+0.15*math.Sin(2*math.Pi*float64(h-15)/24)+rng.NormFloat64()*0.05, 35, 43)*100)/100),
		)
	}
	sid := sourceID
	return httpapi.IcarObservationSummaryResource{
		ResourceType: "icarObservationSummaryResource",
		Animal:       a,
		Meta:         &httpapi.IcarMetaDataType{Source: source, SourceId: &sid, Modified: day.Add(time.Duration(hours[len(hours)-1]+1) * time.Hour)},
		Statistics:   &stats,
	}
}

func stat(m domain.Metric, start time.Time, v float64) httpapi.IcarObservationStatisticsType {
	spec, _ := domain.LookupMetric(m)
	unit, agg, value := spec.Unit, httpapi.IcarAggregationType(spec.Aggregation), math.Round(v*100)/100
	return httpapi.IcarObservationStatisticsType{
		Metric: &httpapi.IcarMetricType{Scheme: m.Scheme, Id: m.ID}, StartDateTime: start,
		Duration: httpapi.PT1H, Unit: &unit, Aggregation: &agg, Value: &value,
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

type client struct {
	base  string
	token string
	http  *http.Client
}

func (c *client) request(ctx context.Context, method, path string, body any, out any) (int, error) {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, r)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(data))
	}
	return resp.StatusCode, json.Unmarshal(data, out)
}

func (c *client) animals(ctx context.Context, loc domain.Location) ([]httpapi.IcarAnimalIdentifierType, error) {
	next := fmt.Sprintf("%s/locations/%s/%s/animals?page-size=500", c.base, url.PathEscape(loc.Scheme), url.PathEscape(loc.ID))
	var out []httpapi.IcarAnimalIdentifierType
	for next != "" {
		var page httpapi.IcarAnimalCoreCollection
		if _, err := c.request(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}
		if page.Member != nil {
			for _, a := range *page.Member {
				if a.Meta == nil || a.Meta.IsDeleted == nil || !*a.Meta.IsDeleted {
					out = append(out, a.Identifier)
				}
			}
		}
		next = ""
		if page.View != nil && page.View.Next != nil {
			next = *page.View.Next
		}
	}
	slices.SortFunc(out, func(a, b httpapi.IcarAnimalIdentifierType) int { return strings.Compare(a.Id, b.Id) })
	return out, nil
}

type report struct {
	mu        sync.Mutex
	latencies []time.Duration
	outcomes  map[string]int
	elapsed   time.Duration
}

func (c *client) post(ctx context.Context, o options, resources []httpapi.IcarObservationSummaryResource) (*report, error) {
	path := fmt.Sprintf("%s/batches/locations/%s/%s/observation-summary-metrics", c.base, url.PathEscape(o.location.Scheme), url.PathEscape(o.location.ID))
	batches := slices.Collect(slices.Chunk(resources, o.batchSize))
	rep := &report{outcomes: map[string]int{}}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	work := make(chan []httpapi.IcarObservationSummaryResource)
	var wg sync.WaitGroup
	start := time.Now()
	for range o.concurrency {
		wg.Go(func() {
			for batch := range work {
				var results httpapi.BatchResults
				t0 := time.Now()
				if _, err := c.request(ctx, http.MethodPost, path, batch, &results); err != nil {
					cancel(err)
					return
				}
				rep.record(time.Since(t0), results)
			}
		})
	}
	for _, b := range batches {
		select {
		case work <- b:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	rep.elapsed = time.Since(start)
	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	return rep, nil
}

func (r *report) record(d time.Duration, results httpapi.BatchResults) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latencies = append(r.latencies, d)
	for _, res := range results {
		outcome := "accepted"
		if res.Messages != nil && len(*res.Messages) > 0 {
			if t := (*res.Messages)[0].Type; t != nil {
				outcome = *t
			}
		}
		r.outcomes[outcome]++
	}
}

func (r *report) print(stats int) {
	slices.Sort(r.latencies)
	pct := func(p float64) time.Duration {
		if len(r.latencies) == 0 {
			return 0
		}
		return r.latencies[int(math.Ceil(p*float64(len(r.latencies))))-1]
	}
	fmt.Printf("outcomes: %v\n", r.outcomes)
	fmt.Printf("%d requests in %s: %.0f statistics/s; request latency p50 %s, p99 %s, max %s\n",
		len(r.latencies), r.elapsed.Round(time.Millisecond), float64(stats)/r.elapsed.Seconds(),
		pct(0.50).Round(time.Millisecond), pct(0.99).Round(time.Millisecond), pct(1).Round(time.Millisecond))
}
