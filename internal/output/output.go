package output

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/infracost/infracost/internal/providers/terraform"
	"github.com/infracost/infracost/internal/schema"
	"github.com/shopspring/decimal"
	"github.com/urfave/cli/v2"
)

type Root struct {
	Resources        []Resource       `json:"resources"`
	TotalHourlyCost  *decimal.Decimal `json:"totalHourlyCost"`
	TotalMonthlyCost *decimal.Decimal `json:"totalMonthlyCost"`
	TimeGenerated    time.Time        `json:"timeGenerated"`
	Warnings         []string         `json:"warnings"`
}

type CostComponent struct {
	Name            string           `json:"name"`
	Unit            string           `json:"unit"`
	HourlyQuantity  *decimal.Decimal `json:"hourlyQuantity"`
	MonthlyQuantity *decimal.Decimal `json:"monthlyQuantity"`
	Price           decimal.Decimal  `json:"price"`
	HourlyCost      *decimal.Decimal `json:"hourlyCost"`
	MonthlyCost     *decimal.Decimal `json:"monthlyCost"`
}

type Resource struct {
	Name           string           `json:"name"`
	HourlyCost     *decimal.Decimal `json:"hourlyCost"`
	MonthlyCost    *decimal.Decimal `json:"monthlyCost"`
	CostComponents []CostComponent  `json:"costComponents,omitempty"`
	SubResources   []Resource       `json:"subresources,omitempty"`
}

func outputResource(r *schema.Resource) Resource {
	comps := make([]CostComponent, 0, len(r.CostComponents))
	for _, c := range r.CostComponents {

		comps = append(comps, CostComponent{
			Name:            c.Name,
			Unit:            c.UnitWithMultiplier(),
			HourlyQuantity:  c.UnitMultiplierHourlyQuantity(),
			MonthlyQuantity: c.UnitMultiplierMonthlyQuantity(),
			Price:           c.UnitMultiplierPrice(),
			HourlyCost:      c.HourlyCost,
			MonthlyCost:     c.MonthlyCost,
		})
	}

	subresources := make([]Resource, 0, len(r.SubResources))
	for _, s := range r.SubResources {
		subresources = append(subresources, outputResource(s))
	}

	return Resource{
		Name:           r.Name,
		HourlyCost:     r.HourlyCost,
		MonthlyCost:    r.MonthlyCost,
		CostComponents: comps,
		SubResources:   subresources,
	}
}

func ToOutputFormat(resources []*schema.Resource, c *cli.Context) Root {
	arr := make([]Resource, 0, len(resources))

	var totalHourlyCost *decimal.Decimal
	var totalMonthlyCost *decimal.Decimal

	for _, r := range resources {
		if r.IsSkipped {
			continue
		}
		arr = append(arr, outputResource(r))

		if r.HourlyCost != nil {
			if totalHourlyCost == nil {
				totalHourlyCost = decimalPtr(decimal.Zero)
			}

			totalHourlyCost = decimalPtr(totalHourlyCost.Add(*r.HourlyCost))
		}
		if r.MonthlyCost != nil {
			if totalMonthlyCost == nil {
				totalMonthlyCost = decimalPtr(decimal.Zero)
			}

			totalMonthlyCost = decimalPtr(totalMonthlyCost.Add(*r.MonthlyCost))
		}
	}

	out := Root{
		Resources:        arr,
		TotalHourlyCost:  totalHourlyCost,
		TotalMonthlyCost: totalMonthlyCost,
		TimeGenerated:    time.Now(),
	}

	msg := skippedResourcesMessage(resources, c.Bool("show-skipped"))
	if msg != "" {
		out.Warnings = append(out.Warnings, msg)
	}

	return out
}

func Load(data []byte) (Root, error) {
	var out Root
	err := json.Unmarshal(data, &out)
	return out, err
}

func Combine(outs ...Root) Root {
	var combined Root

	var totalHourlyCost *decimal.Decimal
	var totalMonthlyCost *decimal.Decimal

	for _, o := range outs {
		combined.Resources = append(combined.Resources, o.Resources...)

		if o.TotalHourlyCost != nil {
			if totalHourlyCost == nil {
				totalHourlyCost = decimalPtr(decimal.Zero)
			}

			totalHourlyCost = decimalPtr(totalHourlyCost.Add(*o.TotalHourlyCost))
		}
		if o.TotalMonthlyCost != nil {
			if totalMonthlyCost == nil {
				totalMonthlyCost = decimalPtr(decimal.Zero)
			}

			totalMonthlyCost = decimalPtr(totalMonthlyCost.Add(*o.TotalMonthlyCost))
		}
	}

	sortResources(combined)

	combined.TotalHourlyCost = totalHourlyCost
	combined.TotalMonthlyCost = totalMonthlyCost
	combined.TimeGenerated = time.Now()

	return combined
}

func sortResources(out Root) {
	sort.Slice(out.Resources, func(i, j int) bool {
		return out.Resources[i].Name < out.Resources[j].Name
	})
}

func skippedResourcesMessage(resources []*schema.Resource, showDetails bool) string {
	summary := schema.GenerateResourceSummary(resources)
	if summary.TotalUnsupported == 0 {
		return ""
	}

	supportedTypeCount := 0
	for rType := range summary.UnsupportedCounts {
		if terraform.HasSupportedProvider(rType) {
			supportedTypeCount++
		}
	}

	message := fmt.Sprintf("%d resource types couldn't be estimated as Infracost doesn't support them yet (https://www.infracost.io/docs/supported_resources)", supportedTypeCount)
	if supportedTypeCount == 1 {
		message = "1 resource type couldn't be estimated as Infracost doesn't support it yet (https://www.infracost.io/docs/supported_resources)"
	}

	if showDetails {
		message += ".\n"
	} else {
		message += ", re-run with --show-skipped to see the list.\n"
	}

	message += "We're continually adding new resources, please email hello@infracost.io if you'd like us to prioritize your list."

	if showDetails {
		for rType, count := range summary.UnsupportedCounts {
			if terraform.HasSupportedProvider(rType) {
				message += fmt.Sprintf("\n%d x %s", count, rType)
			}
		}
	}

	return message
}

func formatAmount(d decimal.Decimal) string {
	f, _ := d.Float64()
	if f < 0.00005 && f != 0 {
		return fmt.Sprintf("%.g", f)
	}

	return humanize.FormatFloat("#,###.####", f)
}

func formatCost(d *decimal.Decimal) string {
	if d == nil {
		return "-"
	}
	return formatAmount(*d)
}

func formatQuantity(q *decimal.Decimal) string {
	if q == nil {
		return "-"
	}
	f, _ := q.Float64()
	return humanize.CommafWithDigits(f, 4)
}

func decimalPtr(d decimal.Decimal) *decimal.Decimal {
	return &d
}
