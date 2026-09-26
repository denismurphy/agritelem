package pipeline

import (
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
)

var day0 = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

func d(n int) time.Time { return day0.AddDate(0, 0, n) }

func agg(m domain.Metric, day, hours int, sum float64) DailyAggregate {
	return DailyAggregate{Metric: m, Day: d(day), Hours: hours, Sum: sum}
}

func deriveOne(t *testing.T, aggs []DailyAggregate, day int, m domain.Metric) (DailyStatistic, bool) {
	t.Helper()
	got, err := Derive(aggs, d(day), d(day))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, s := range got {
		if s.Metric == m {
			return s, true
		}
	}
	return DailyStatistic{}, false
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDeriveCoverageAndQuality(t *testing.T) {
	tests := []struct {
		name           string
		hours          int
		sum            float64
		wantPresent    bool
		wantValue      float64
		wantQuality    Quality
		wantIncomplete bool
	}{
		{"full day", 24, 480, true, 480, QualityVerified, false},
		{"21 of 24 hours is above the 0.85 threshold", 21, 420, true, 480, QualityVerified, true},
		{"20 of 24 hours is below it", 20, 400, true, 480, QualityImputed, true},
		{"12 of 24 hours is the minimum", 12, 240, true, 480, QualityImputed, true},
		{"11 of 24 hours is omitted", 11, 220, false, 0, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, ok := deriveOne(t, []DailyAggregate{agg(domain.RuminationTime, 0, tt.hours, tt.sum)}, 0, domain.RuminationTime)
			if ok != tt.wantPresent {
				t.Fatalf("present = %v, want %v", ok, tt.wantPresent)
			}
			if !ok {
				return
			}
			if !approx(s.Value, tt.wantValue) || s.Quality != tt.wantQuality || s.Incomplete != tt.wantIncomplete {
				t.Errorf("got value %v quality %q incomplete %v, want %v %q %v",
					s.Value, s.Quality, s.Incomplete, tt.wantValue, tt.wantQuality, tt.wantIncomplete)
			}
			if !approx(s.Coverage, float64(tt.hours)/24) {
				t.Errorf("coverage = %v, want %v", s.Coverage, float64(tt.hours)/24)
			}
		})
	}
}

func TestDeriveAveragesAverageMetrics(t *testing.T) {
	s, _ := deriveOne(t, []DailyAggregate{agg(domain.ReticularTemperature, 0, 20, 20*38.5)}, 0, domain.ReticularTemperature)
	if !approx(s.Value, 38.5) {
		t.Errorf("value = %v, want the hourly mean 38.5, not a scaled sum", s.Value)
	}
}

// history returns rumination days -14..-1 with values 1..14 (full coverage).
func history() []DailyAggregate {
	var out []DailyAggregate
	for i := 1; i <= 14; i++ {
		out = append(out, agg(domain.RuminationTime, i-15, 24, float64(i)))
	}
	return out
}

func TestDeriveRobustBaseline(t *testing.T) {
	// Values 1..14: median 7.5; absolute deviations 0.5..6.5 twice each, so
	// MAD = 3.5 and z(20) = (20 - 7.5) / (1.4826 * 3.5).
	aggs := append(history(), agg(domain.RuminationTime, 0, 24, 20))
	s, _ := deriveOne(t, aggs, 0, domain.RuminationTime)
	if s.BaselineMedian == nil || !approx(*s.BaselineMedian, 7.5) {
		t.Fatalf("baseline = %v, want 7.5", s.BaselineMedian)
	}
	if want := 12.5 / (1.4826 * 3.5); s.ZScore == nil || !approx(*s.ZScore, want) {
		t.Fatalf("z = %v, want %v", s.ZScore, want)
	}
}

func TestDeriveBaselineWindow(t *testing.T) {
	t.Run("needs seven prior days", func(t *testing.T) {
		aggs := append(history()[8:], agg(domain.RuminationTime, 0, 24, 20)) // 6 prior days
		s, _ := deriveOne(t, aggs, 0, domain.RuminationTime)
		if s.BaselineMedian != nil || s.ZScore != nil {
			t.Errorf("baseline from 6 days: median %v z %v, want nil", s.BaselineMedian, s.ZScore)
		}
	})
	t.Run("ignores days older than fourteen", func(t *testing.T) {
		aggs := append(history(), agg(domain.RuminationTime, -15, 24, 700), agg(domain.RuminationTime, 0, 24, 20))
		s, _ := deriveOne(t, aggs, 0, domain.RuminationTime)
		if s.BaselineMedian == nil || !approx(*s.BaselineMedian, 7.5) {
			t.Errorf("baseline = %v, want 7.5 (day -15 excluded)", s.BaselineMedian)
		}
	})
	t.Run("ignores omitted days", func(t *testing.T) {
		h := history()
		h[13] = agg(domain.RuminationTime, -1, 11, 700) // below minimum coverage
		s, _ := deriveOne(t, append(h, agg(domain.RuminationTime, 0, 24, 20)), 0, domain.RuminationTime)
		if s.BaselineMedian == nil || !approx(*s.BaselineMedian, 7) {
			t.Errorf("baseline = %v, want 7 (median of 1..13)", s.BaselineMedian)
		}
	})
	t.Run("zero MAD gives no z-score", func(t *testing.T) {
		var aggs []DailyAggregate
		for i := -10; i < 0; i++ {
			aggs = append(aggs, agg(domain.RuminationTime, i, 24, 500))
		}
		s, _ := deriveOne(t, append(aggs, agg(domain.RuminationTime, 0, 24, 400)), 0, domain.RuminationTime)
		if s.BaselineMedian == nil || *s.BaselineMedian != 500 || s.ZScore != nil {
			t.Errorf("median %v z %v, want 500 and nil", s.BaselineMedian, s.ZScore)
		}
	})
}

// estrusFixture: 14 steady days of rumination (~480) and steps (~3000),
// then day 0 with the given rumination and step totals.
func estrusFixture(rumination, steps float64) []DailyAggregate {
	var aggs []DailyAggregate
	for i := -14; i < 0; i++ {
		wobble := float64(i%3) * 10
		aggs = append(aggs, agg(domain.RuminationTime, i, 24, 480+wobble), agg(domain.StepCount, i, 24, 3000+wobble*10))
	}
	return append(aggs, agg(domain.RuminationTime, 0, 24, rumination), agg(domain.StepCount, 0, 24, steps))
}

func TestDeriveHeatSignature(t *testing.T) {
	tests := []struct {
		name       string
		rumination float64
		steps      float64
		want       bool
	}{
		{"rumination drop with step spike", 380, 6000, true},
		{"step spike alone", 480, 6000, false},
		{"rumination drop alone", 380, 3000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aggs := estrusFixture(tt.rumination, tt.steps)
			for _, m := range []domain.Metric{domain.RuminationTime, domain.StepCount} {
				s, _ := deriveOne(t, aggs, 0, m)
				if got := slices.Contains(s.Flags, FlagHeatSignature); got != tt.want {
					t.Errorf("%s flagged = %v, want %v (z = %v)", m.ID, got, tt.want, deref(s.ZScore))
				}
			}
		})
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestDeriveSingleMetricFlags(t *testing.T) {
	t.Run("time budget conflict", func(t *testing.T) {
		aggs := []DailyAggregate{agg(domain.EatingTime, 0, 24, 500), agg(domain.LyingTime, 0, 24, 1000)}
		for _, m := range []domain.Metric{domain.EatingTime, domain.LyingTime} {
			s, _ := deriveOne(t, aggs, 0, m)
			if !slices.Contains(s.Flags, FlagTimeBudgetConflict) {
				t.Errorf("%s not flagged for 1500 min of eating + lying", m.ID)
			}
		}
		ok, _ := deriveOne(t, []DailyAggregate{agg(domain.EatingTime, 0, 24, 440), agg(domain.LyingTime, 0, 24, 1000)}, 0, domain.EatingTime)
		if slices.Contains(ok.Flags, FlagTimeBudgetConflict) {
			t.Error("flagged exactly 1440 minutes")
		}
	})
	t.Run("thermal stress at threshold", func(t *testing.T) {
		hot, _ := deriveOne(t, []DailyAggregate{agg(domain.ReticularTemperature, 0, 24, 24*39.5)}, 0, domain.ReticularTemperature)
		cool, _ := deriveOne(t, []DailyAggregate{agg(domain.ReticularTemperature, 0, 24, 24*39.4)}, 0, domain.ReticularTemperature)
		if !slices.Contains(hot.Flags, FlagThermalStress) || slices.Contains(cool.Flags, FlagThermalStress) {
			t.Errorf("39.5 flags %v, 39.4 flags %v", hot.Flags, cool.Flags)
		}
	})
	t.Run("daily rumination above the spec's 720 min", func(t *testing.T) {
		s, _ := deriveOne(t, []DailyAggregate{agg(domain.RuminationTime, 0, 24, 750)}, 0, domain.RuminationTime)
		if !slices.Contains(s.Flags, FlagDailyOutOfRange) {
			t.Errorf("flags = %v, want %s", s.Flags, FlagDailyOutOfRange)
		}
	})
}

func TestDeriveReturnsOnlyRequestedDaysInOrder(t *testing.T) {
	aggs := append(history(), agg(domain.StepCount, 0, 24, 100), agg(domain.RuminationTime, 0, 24, 20), agg(domain.RuminationTime, 1, 24, 21), agg(domain.RuminationTime, 2, 24, 22))
	got, err := Derive(aggs, d(0), d(1))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, s := range got {
		keys = append(keys, s.Day.Format("01-02")+" "+s.Metric.ID)
	}
	want := []string{"08-01 RuminationTime", "08-01 StepCount", "08-02 RuminationTime"}
	if !slices.Equal(keys, want) {
		t.Errorf("got %v, want %v", keys, want)
	}
}

func TestDeriveIsIndependentOfInputOrder(t *testing.T) {
	aggs := append(estrusFixture(380, 6000), agg(domain.LyingTime, 0, 20, 700))
	want, err := Derive(aggs, d(-3), d(0))
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20 {
		shuffled := slices.Clone(aggs)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got, err := Derive(shuffled, d(-3), d(0))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("output depends on input order")
		}
	}
}

func TestDeriveRejectsCorruptAggregates(t *testing.T) {
	tests := []struct {
		name string
		aggs []DailyAggregate
		want string
	}{
		{"zero hours", []DailyAggregate{agg(domain.RuminationTime, 0, 0, 0)}, "hours"},
		{"25 hours", []DailyAggregate{agg(domain.RuminationTime, 0, 25, 0)}, "hours"},
		{"not midnight", []DailyAggregate{{Metric: domain.RuminationTime, Day: d(0).Add(time.Hour), Hours: 24}}, "midnight"},
		{"duplicate day", []DailyAggregate{agg(domain.RuminationTime, 0, 24, 1), agg(domain.RuminationTime, 0, 24, 2)}, "duplicate"},
		{"unknown metric", []DailyAggregate{{Metric: domain.Metric{Scheme: "x", ID: "y"}, Day: d(0), Hours: 24}}, "not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Derive(tt.aggs, d(0), d(0))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want mention of %q", err, tt.want)
			}
		})
	}
}
