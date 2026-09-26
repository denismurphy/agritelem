package httpapi

import (
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/pipeline"
	"github.com/denismurphy/agritelem/backend/internal/service"
)

// ICAR resourceType values.
const (
	resourceAnimal  = "icarAnimalCoreResource"
	resourceDevice  = "icarDeviceResource"
	resourceRemark  = "icarRemarkEventResource"
	resourceSummary = "icarObservationSummaryResource"
)

// wire formats a timestamp for the API: UTC with whole seconds, which the
// spec requires (YYYY-MM-DDTHH:MM:SSZ) and every client can parse.
func wire(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func location(scheme, id string) domain.Location { return domain.Location{Scheme: scheme, ID: id} }

func identifier(a domain.AnimalID) IcarAnimalIdentifierType {
	return IcarAnimalIdentifierType{Scheme: a.Scheme, Id: a.ID}
}

func locationIdentifier(l domain.Location) *IcarLocationIdentifierType {
	return &IcarLocationIdentifierType{Scheme: l.Scheme, Id: l.ID}
}

func metaOut(m domain.Meta) *IcarMetaDataType {
	out := &IcarMetaDataType{Source: m.Source, Modified: wire(m.Modified)}
	if m.SourceID != "" {
		out.SourceId = &m.SourceID
	}
	if m.Creator != "" {
		out.Creator = &m.Creator
	}
	return out
}

func animalOut(a domain.Animal) IcarAnimalCoreResource {
	meta := metaOut(a.Meta)
	if a.Deleted {
		deleted := true
		meta.IsDeleted = &deleted
	}
	status := IcarAnimalStatusType(a.Status)
	out := IcarAnimalCoreResource{
		ResourceType:  resourceAnimal,
		Identifier:    identifier(a.ID),
		Location:      locationIdentifier(a.Location),
		Specie:        IcarAnimalSpecieType(a.Specie),
		Gender:        IcarAnimalGenderType(a.Gender),
		Name:          a.Name,
		ManagementTag: a.ManagementTag,
		Status:        &status,
		Meta:          meta,
	}
	if a.BirthDate != nil {
		b := wire(*a.BirthDate)
		out.BirthDate = &b
	}
	if a.Breed != nil {
		out.PrimaryBreed = &IcarBreedIdentifierType{Scheme: a.Breed.Scheme, Id: a.Breed.ID}
	}
	return out
}

func deviceOut(d domain.Device, loc domain.Location) IcarDeviceResource {
	out := IcarDeviceResource{
		ResourceType: resourceDevice,
		Id:           d.ID,
		Serial:       d.Serial,
		Name:         d.Name,
		Location:     locationIdentifier(loc),
		Meta:         &IcarMetaDataType{Source: "agritelem", Modified: wire(d.Modified)},
	}
	active := d.Assignment != nil
	out.IsActive = &active
	if a := d.Assignment; a != nil {
		animal := identifier(a.Animal)
		out.Animal = &animal
		from := wire(a.ValidFrom)
		out.Meta.ValidFrom = &from
		if a.ValidTo != nil {
			to := wire(*a.ValidTo)
			out.Meta.ValidTo = &to
		}
	}
	return out
}

func remarkOut(r domain.Remark) IcarRemarkEventResource {
	id := r.ID
	note := r.Note
	event := wire(r.EventDateTime)
	category := AgritelemRemarkCategory(r.Category)
	return IcarRemarkEventResource{
		ResourceType:  resourceRemark,
		Id:            &id,
		Animal:        identifier(r.Animal),
		Location:      locationIdentifier(r.Location),
		EventDateTime: &event,
		Note:          &note,
		Responsible:   r.Responsible,
		Meta:          metaOut(r.Meta),
		Agritelem:     &AgritelemRemarkExtension{Category: &category},
	}
}

func remarkIn(in IcarRemarkEventResource) domain.RawRemark {
	raw := domain.RawRemark{
		AnimalScheme:  in.Animal.Scheme,
		AnimalID:      in.Animal.Id,
		EventDateTime: in.EventDateTime,
		Note:          in.Note,
		Responsible:   in.Responsible,
	}
	if in.Agritelem != nil && in.Agritelem.Category != nil {
		c := string(*in.Agritelem.Category)
		raw.Category = &c
	}
	if m := in.Meta; m != nil {
		raw.Source = &m.Source
		raw.SourceID = m.SourceId
		raw.Creator = m.Creator
		modified := m.Modified
		raw.Modified = &modified
	}
	return raw
}

func summaryIn(in IcarObservationSummaryResource) pipeline.RawSummary {
	raw := pipeline.RawSummary{AnimalScheme: in.Animal.Scheme, AnimalID: in.Animal.Id}
	if m := in.Meta; m != nil {
		raw.Meta = &pipeline.RawMeta{Source: m.Source, SourceID: m.SourceId, Creator: m.Creator, Modified: m.Modified}
	}
	if in.Statistics != nil {
		for _, s := range *in.Statistics {
			st := pipeline.RawStatistic{Start: s.StartDateTime, Duration: string(s.Duration), Unit: s.Unit, Value: s.Value}
			if s.Metric != nil {
				st.MetricScheme, st.MetricID = s.Metric.Scheme, s.Metric.Id
			}
			if s.Aggregation != nil {
				a := string(*s.Aggregation)
				st.Aggregation = &a
			}
			raw.Statistics = append(raw.Statistics, st)
		}
	}
	return raw
}

func statisticOut(metric domain.Metric, start time.Time, duration IcarDurationType, value float64) IcarObservationStatisticsType {
	spec, _ := domain.LookupMetric(metric) // stored metrics were validated at ingest
	unit := spec.Unit
	aggregation := IcarAggregationType(spec.Aggregation)
	v := value
	return IcarObservationStatisticsType{
		Metric:        &IcarMetricType{Scheme: metric.Scheme, Id: metric.ID},
		StartDateTime: wire(start),
		Duration:      duration,
		Unit:          &unit,
		Aggregation:   &aggregation,
		Value:         &v,
	}
}

func summaryOut(s service.Summary, loc domain.Location) IcarObservationSummaryResource {
	stats := make([]IcarObservationStatisticsType, 0, len(s.Daily)+len(s.Hourly))
	for _, d := range s.Daily {
		st := statisticOut(d.Metric, d.Day, P1D, d.Value)
		incomplete := d.Incomplete
		st.IsIncomplete = &incomplete
		coverage := d.Coverage
		quality := AgritelemDataQuality(d.Quality)
		flags := make([]AgritelemFlag, len(d.Flags))
		for i, f := range d.Flags {
			flags[i] = AgritelemFlag(f)
		}
		st.Agritelem = &AgritelemStatisticsExtension{
			Coverage: &coverage, DataQuality: &quality, BaselineMedian: d.BaselineMedian, ZScore: d.ZScore, Flags: &flags,
		}
		stats = append(stats, st)
	}
	for _, h := range s.Hourly {
		stats = append(stats, statisticOut(h.Metric, h.Start, PT1H, h.Value))
	}
	return IcarObservationSummaryResource{
		ResourceType: resourceSummary,
		Animal:       identifier(s.Animal),
		Location:     locationIdentifier(loc),
		Statistics:   &stats,
	}
}

func herdOut(h service.HerdOverview) AgritelemHerdOverview {
	out := AgritelemHerdOverview{
		AsOf:    wire(h.AsOf),
		Day:     openapi_types.Date{Time: h.Day},
		Animals: make([]AgritelemHerdAnimal, len(h.Animals)),
	}
	out.Kpis.AnimalCount = len(h.Animals)
	out.Kpis.EstrusAlerts = h.EstrusAlerts
	out.Kpis.HeatStress = h.HeatStress
	out.Kpis.VeterinaryAttention = h.VeterinaryAttention
	out.Kpis.MeanRuminationMinutes = h.MeanRuminationMinutes
	for i, a := range h.Animals {
		statuses := make([]AgritelemAnimalStatus, len(a.Statuses))
		for j, s := range a.Statuses {
			statuses[j] = AgritelemAnimalStatus(s)
		}
		out.Animals[i] = AgritelemHerdAnimal{
			Animal: identifier(a.Animal.ID), ManagementTag: a.Animal.ManagementTag,
			Statuses: statuses, RuminationMinutes: a.RuminationMinutes,
		}
	}
	return out
}
