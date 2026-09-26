package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/auth"
	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/service"
	"github.com/denismurphy/agritelem/backend/internal/store"
)

// MaxBatchResources bounds the resources in one batch request.
const MaxBatchResources = 5000

// handlers implements StrictServerInterface.
type handlers struct {
	svc       *service.Service
	publicURL *url.URL
}

var _ StrictServerInterface = (*handlers)(nil)

// authorize returns the caller if it holds scope for loc.
func authorize(ctx context.Context, scope string, loc domain.Location) (auth.Principal, error) {
	p, ok := auth.FromContext(ctx)
	if !ok {
		return auth.Principal{}, errors.New("no principal in context: authentication middleware not installed")
	}
	if err := p.Authorize(scope, loc); err != nil {
		return auth.Principal{}, err
	}
	return p, nil
}

func unsupported(param string) error {
	return fmt.Errorf("%w: query parameter %s is not supported by this server", errBadRequest, param)
}

func (h *handlers) GetAnimals(ctx context.Context, req GetAnimalsRequestObject) (GetAnimalsResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	if _, err := authorize(ctx, auth.ScopeAnimalsRead, loc); err != nil {
		return nil, err
	}
	after, err := decodePageToken(req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var from time.Time
	if req.Params.MetaModifiedFrom != nil {
		from = *req.Params.MetaModifiedFrom
	}
	size := pageSize(req.Params.PageSize)
	animals, err := h.svc.ListAnimals(ctx, loc, from, after, size+1)
	if err != nil {
		return nil, err
	}
	view := &IcarAnimalCoreCollection_View{PageSize: &size}
	if len(animals) > size {
		animals = animals[:size]
		last := animals[size-1]
		next := nextURL(h.publicURL, fmt.Sprintf("/locations/%s/%s/animals", url.PathEscape(loc.Scheme), url.PathEscape(loc.ID)),
			queryOf(req.Params.MetaModifiedFrom), encodePageToken(store.Cursor{Modified: last.Meta.Modified, Key1: last.ID.Scheme, Key2: last.ID.ID}), size)
		view.Next = &next
	}
	members := make([]IcarAnimalCoreResource, len(animals))
	for i, a := range animals {
		members[i] = animalOut(a)
	}
	return GetAnimals200JSONResponse{Member: &members, View: view}, nil
}

func queryOf(modifiedFrom *time.Time) url.Values {
	q := url.Values{}
	if modifiedFrom != nil {
		q.Set("meta-modified-from", modifiedFrom.UTC().Format(time.RFC3339Nano))
	}
	return q
}

func (h *handlers) GetDevices(ctx context.Context, req GetDevicesRequestObject) (GetDevicesResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	if _, err := authorize(ctx, auth.ScopeAnimalsRead, loc); err != nil {
		return nil, err
	}
	if req.Params.MetaModifiedTo != nil {
		return nil, unsupported("meta-modified-to")
	}
	var from time.Time
	if req.Params.MetaModifiedFrom != nil {
		from = *req.Params.MetaModifiedFrom
	}
	devices, err := h.svc.ListDevices(ctx, loc, from)
	if err != nil {
		return nil, err
	}
	members := make([]IcarDeviceResource, len(devices))
	for i, d := range devices {
		members[i] = deviceOut(d, loc)
	}
	return GetDevices200JSONResponse{Member: &members}, nil
}

func (h *handlers) GetAnimalObservationSummaryMetrics(ctx context.Context, req GetAnimalObservationSummaryMetricsRequestObject) (GetAnimalObservationSummaryMetricsResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	if _, err := authorize(ctx, auth.ScopeAnimalsRead, loc); err != nil {
		return nil, err
	}
	p := req.Params
	if p.MetaModifiedFrom != nil || p.MetaModifiedTo != nil {
		return nil, unsupported("meta-modified-from/meta-modified-to")
	}
	if p.AnimalScheme == nil || p.AnimalId == nil {
		return nil, fmt.Errorf("%w: animal-scheme and animal-id are required", errBadRequest)
	}
	animal, err := domain.ParseAnimalID(*p.AnimalScheme, *p.AnimalId)
	if err != nil {
		return nil, err
	}
	q := service.SummaryQuery{Animal: animal, Duration: p.Duration}
	// The ICAR component names of date-from and date-to are swapped upstream;
	// the generated field names follow the wire names, which are correct.
	if p.DateFrom != nil {
		q.From = &p.DateFrom.Time
	}
	if p.DateTo != nil {
		q.To = &p.DateTo.Time
	}
	if (p.MetricScheme == nil) != (p.MetricId == nil) {
		return nil, fmt.Errorf("%w: metric-scheme and metric-id must be given together", errBadRequest)
	}
	if p.MetricScheme != nil {
		q.Metric = &domain.Metric{Scheme: *p.MetricScheme, ID: *p.MetricId}
	}
	summary, err := h.svc.Summaries(ctx, loc, q)
	if err != nil {
		return nil, err
	}
	members := []IcarObservationSummaryResource{summaryOut(summary, loc)}
	return GetAnimalObservationSummaryMetrics200JSONResponse{Member: &members}, nil
}

func (h *handlers) PostBatchObservationSummaryMetrics(ctx context.Context, req PostBatchObservationSummaryMetricsRequestObject) (PostBatchObservationSummaryMetricsResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	principal, err := authorize(ctx, auth.ScopeObservationsWrite, loc)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, fmt.Errorf("%w: request body is required", errBadRequest)
	}
	resources := *req.Body
	if len(resources) > MaxBatchResources {
		return PostBatchObservationSummaryMetricsdefaultJSONResponse{StatusCode: 413, Body: BatchResults{{
			Messages: &[]IcarResponseMessageResource{message(413, typeTooLarge, "Batch too large",
				fmt.Sprintf("%d resources exceed the limit of %d per batch", len(resources), MaxBatchResources), "")},
		}}}, nil
	}

	raws := make([]service.RawResource, len(resources))
	for i, r := range resources {
		body, err := json.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("re-encode resource %d: %w", i, err)
		}
		raws[i] = service.RawResource{Summary: summaryIn(r), JSON: body}
	}
	results, err := h.svc.IngestSummaries(ctx, principal.Subject, loc, raws)
	if err != nil {
		return nil, err
	}

	out := make(BatchResults, len(results))
	for i, r := range results {
		br := IcarBatchResult{Meta: resources[i].Meta}
		if r.ResourceID != "" {
			id := r.ResourceID
			br.Id = &id
		}
		switch r.Status {
		case service.IngestAccepted:
		case service.IngestReplayed:
			info := Information
			typ, title, detail := "REPLAYED", "Already stored", "an identical resource with this source and sourceId was stored earlier; nothing changed"
			br.Messages = &[]IcarResponseMessageResource{{Type: &typ, Severity: &info, Title: &title, Detail: &detail}}
		case service.IngestConflict:
			msgs := problemMessages(409, r.Problems)
			br.Messages = &msgs
		case service.IngestRejected:
			msgs := problemMessages(422, r.Problems)
			br.Messages = &msgs
		}
		out[i] = br
	}
	return PostBatchObservationSummaryMetrics200JSONResponse(out), nil
}

func (h *handlers) GetRemarkEvents(ctx context.Context, req GetRemarkEventsRequestObject) (GetRemarkEventsResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	if _, err := authorize(ctx, auth.ScopeRemarksRead, loc); err != nil {
		return nil, err
	}
	p := req.Params
	switch {
	case p.DateFrom != nil || p.DateTo != nil:
		return nil, unsupported("date-from/date-to")
	case p.MetaModifiedTo != nil:
		return nil, unsupported("meta-modified-to")
	case (p.IdentifierScheme == nil) != (p.IdentifierId == nil):
		return nil, fmt.Errorf("%w: identifier-scheme and identifier-id must be given together", errBadRequest)
	}
	var animal *domain.AnimalID
	if p.IdentifierScheme != nil {
		a, err := domain.ParseAnimalID(*p.IdentifierScheme, *p.IdentifierId)
		if err != nil {
			return nil, err
		}
		animal = &a
	}
	after, err := decodePageToken(p.PageToken)
	if err != nil {
		return nil, err
	}
	var from time.Time
	if p.MetaModifiedFrom != nil {
		from = *p.MetaModifiedFrom
	}
	size := pageSize(p.PageSize)
	remarks, err := h.svc.ListRemarks(ctx, loc, from, animal, after, size+1)
	if err != nil {
		return nil, err
	}
	view := &IcarRemarkEventCollection_View{PageSize: &size}
	if len(remarks) > size {
		remarks = remarks[:size]
		last := remarks[size-1]
		q := queryOf(p.MetaModifiedFrom)
		if animal != nil {
			q.Set("identifier-scheme", animal.Scheme)
			q.Set("identifier-id", animal.ID)
		}
		next := nextURL(h.publicURL, fmt.Sprintf("/locations/%s/%s/remarks", url.PathEscape(loc.Scheme), url.PathEscape(loc.ID)),
			q, encodePageToken(store.Cursor{Modified: last.Meta.Modified, Key1: last.ID}), size)
		view.Next = &next
	}
	members := make([]IcarRemarkEventResource, len(remarks))
	for i, r := range remarks {
		members[i] = remarkOut(r)
	}
	return GetRemarkEvents200JSONResponse{Member: &members, View: view}, nil
}

func (h *handlers) PostSingleRemarkEvent(ctx context.Context, req PostSingleRemarkEventRequestObject) (PostSingleRemarkEventResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	principal, err := authorize(ctx, auth.ScopeRemarksWrite, loc)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, fmt.Errorf("%w: request body is required", errBadRequest)
	}
	result, problems, err := h.svc.CreateRemark(ctx, principal.Subject, loc, remarkIn(*req.Body))
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		msgs := problemMessages(422, problems)
		return PostSingleRemarkEventdefaultJSONResponse{StatusCode: 422, Body: IcarErrorCollection{Errors: &msgs}}, nil
	}
	switch result.Status {
	case store.RemarkCreated, store.RemarkReplayed:
		return PostSingleRemarkEvent200JSONResponse(remarkOut(result.Remark)), nil
	case store.RemarkConflict:
		return PostSingleRemarkEvent409JSONResponse{
			Current: remarkOut(result.Remark),
			Errors: problemMessages(409, []*domain.Problem{{Code: domain.CodeConflictingResubmission, Field: "/meta/sourceId",
				Detail: "a remark with this source and sourceId exists with different content"}}),
		}, nil
	default:
		return nil, fmt.Errorf("unknown remark status %d", result.Status)
	}
}

func (h *handlers) GetHerdOverview(ctx context.Context, req GetHerdOverviewRequestObject) (GetHerdOverviewResponseObject, error) {
	loc := location(req.LocationScheme, req.LocationId)
	if _, err := authorize(ctx, auth.ScopeAnimalsRead, loc); err != nil {
		return nil, err
	}
	overview, err := h.svc.HerdOverview(ctx, loc)
	if err != nil {
		return nil, err
	}
	return GetHerdOverview200JSONResponse(herdOut(overview)), nil
}
