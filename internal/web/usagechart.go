package web

import (
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gamemaker1/markspace/internal/core"
)

// Geometry of the usage chart in CSS pixels. The plot area starts at
// chartPadLeft and chartPadTop and is chartPlotH tall.
const (
	chartPadLeft   = 46
	chartPadRight  = 24
	chartPadTop    = 12
	chartPlotH     = 180
	chartPadBottom = 30
	chartMaxBar    = 24
	chartInset     = 8
	chartMinSlot   = 12
	chartMaxSlot   = 96
	chartMaxDays   = 400
	chartSlots     = 7
	chartTicks     = 4
	chartGap       = 2
	chartRadius    = 4
	chartLabelGap  = 52
)

// chartSeries is one stacked layer of the usage chart.
type chartSeries struct {
	Key   string
	Label string
	// Class names the colour slot, uc-s1 to uc-s7, or uc-other for the
	// layer that folds every series without a slot.
	Class string
	Total int64
}

// chartDay is one bar. Values is aligned with usageChart.Series.
type chartDay struct {
	Date   time.Time
	Values []int64
	Total  int64
}

// chartSegment is the geometry of one layer of one bar, before the gap
// between layers is taken off.
type chartSegment struct {
	Series int
	Y, H   float64
}

// usageChart is a stacked daily token chart rendered as inline SVG.
type usageChart struct {
	Series []chartSeries
	Days   []chartDay
	// Max is the top of the scale. Ticks run from 0 to Max in equal steps.
	Max   int64
	Ticks []int64
	// Slot is the width of one day.
	Slot   int
	Width  int
	Height int
	// Label is the accessible name of the chart and the caption of its
	// table.
	Label string
	// SVG is the scrolling plot. Axis holds the tick labels and stays in
	// place beside it, so the scale is readable at any scroll position.
	SVG  template.HTML
	Axis template.HTML
}

// chartOptions configures buildUsageChart.
type chartOptions struct {
	// PlotWidth is the width the bars fill when the day count allows it.
	PlotWidth int
	// Fixed maps series keys to colour slots 1 to 7 that stay the same for
	// every range.
	Fixed map[string]int
	// Label is the accessible name of the chart.
	Label string
}

// buildUsageChart sums the tokens of report rows into one bar per UTC day
// from from to to, exclusive. key names the layer of a row and its label.
// Rows outside the range and rolled-up months, whose day is YYYY-MM, are
// skipped. Series without a free colour slot fold into one Other layer.
func buildUsageChart(rows []core.AIUsageRow, from, to time.Time, key func(core.AIUsageRow) (string, string), opts chartOptions) *usageChart {
	c := &usageChart{Label: opts.Label}
	index := map[string]int{}
	for d := usageDay(from); d.Before(to) && len(c.Days) < chartMaxDays; d = d.AddDate(0, 0, 1) {
		index[d.Format(time.DateOnly)] = len(c.Days)
		c.Days = append(c.Days, chartDay{Date: d})
	}

	type layer struct {
		key, label string
		total      int64
		days       map[int]int64
	}
	layers := map[string]*layer{}
	var keys []string
	for _, row := range rows {
		i, ok := index[row.Day]
		tokens := row.InputTokens + row.OutputTokens
		if !ok || tokens <= 0 {
			continue
		}
		k, label := key(row)
		l := layers[k]
		if l == nil {
			l = &layer{key: k, label: label, days: map[int]int64{}}
			layers[k] = l
			keys = append(keys, k)
		}
		l.total += tokens
		l.days[i] += tokens
	}
	sort.SliceStable(keys, func(a, b int) bool {
		la, lb := layers[keys[a]], layers[keys[b]]
		if la.total != lb.total {
			return la.total > lb.total
		}
		return la.key < lb.key
	})

	// Fixed slots go first, so a known series keeps its colour. The rest
	// take the free slots in order of tokens.
	slotOf := map[string]int{}
	var taken [chartSlots + 1]bool
	for _, k := range keys {
		if s, ok := opts.Fixed[k]; ok && s >= 1 && s <= chartSlots && !taken[s] {
			slotOf[k] = s
			taken[s] = true
		}
	}
	for _, k := range keys {
		if _, ok := slotOf[k]; ok {
			continue
		}
		for s := 1; s <= chartSlots; s++ {
			if !taken[s] {
				slotOf[k] = s
				taken[s] = true
				break
			}
		}
	}
	bySlot := make([]*layer, chartSlots+1)
	var other *layer
	for _, k := range keys {
		if s, ok := slotOf[k]; ok {
			bySlot[s] = layers[k]
			continue
		}
		if other == nil {
			other = &layer{label: "Other", days: map[int]int64{}}
		}
		other.total += layers[k].total
		for i, v := range layers[k].days {
			other.days[i] += v
		}
	}
	var order []*layer
	for s := 1; s <= chartSlots; s++ {
		if l := bySlot[s]; l != nil {
			order = append(order, l)
			c.Series = append(c.Series, chartSeries{Key: l.key, Label: l.label, Class: "uc-s" + strconv.Itoa(s), Total: l.total})
		}
	}
	if other != nil {
		order = append(order, other)
		c.Series = append(c.Series, chartSeries{Label: other.label, Class: "uc-other", Total: other.total})
	}

	var peak int64
	for i := range c.Days {
		d := &c.Days[i]
		d.Values = make([]int64, len(order))
		for k, l := range order {
			d.Values[k] = l.days[i]
			d.Total += l.days[i]
		}
		peak = max(peak, d.Total)
	}
	var step int64
	c.Max, step = niceScale(peak)
	c.Ticks = []int64{0}
	for t := step; step > 0 && t <= c.Max; t += step {
		c.Ticks = append(c.Ticks, t)
	}
	c.Slot = min(max(opts.PlotWidth/max(len(c.Days), 1), chartMinSlot), chartMaxSlot)
	c.Width = chartInset + len(c.Days)*c.Slot + chartPadRight
	c.Height = chartPadTop + chartPlotH + chartPadBottom
	c.SVG = c.render()
	c.Axis = c.renderAxis()
	return c
}

// niceScale returns the top of a scale that holds peak and the tick step.
// The step is 1, 2, 2.5 or 5 times a power of ten, never below 1, and the
// scale has at most chartTicks steps.
func niceScale(peak int64) (top, step int64) {
	if peak <= 0 {
		return 0, 0
	}
	raw := float64(peak) / chartTicks
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	s := 10 * mag
	for _, m := range []float64{1, 2, 2.5, 5} {
		// 2.5 below 10 is not a whole number of tokens.
		if m == 2.5 && mag < 10 {
			continue
		}
		if m*mag >= raw {
			s = m * mag
			break
		}
	}
	step = max(int64(math.Round(s)), 1)
	top = (peak + step - 1) / step * step
	return top, step
}

// y maps a token count onto the vertical pixel position. Bars, gridlines and
// tick labels all use it.
func (c *usageChart) y(v int64) float64 {
	base := float64(chartPadTop + chartPlotH)
	if c.Max <= 0 {
		return base
	}
	return base - float64(v)/float64(c.Max)*chartPlotH
}

// segments returns the layers of day i from the baseline up. A layer with
// no tokens has no segment.
func (c *usageChart) segments(i int) []chartSegment {
	var out []chartSegment
	var cum int64
	for k, v := range c.Days[i].Values {
		if v <= 0 {
			continue
		}
		bottom := c.y(cum)
		cum += v
		top := c.y(cum)
		out = append(out, chartSegment{Series: k, Y: top, H: bottom - top})
	}
	return out
}

// describe is the hover text of day i.
func (c *usageChart) describe(i int) string {
	d := c.Days[i]
	var b strings.Builder
	b.WriteString(d.Date.Format("Mon 2 Jan 2006") + ": " + usageNum(d.Total) + " tokens")
	for k, v := range d.Values {
		if v > 0 {
			b.WriteString("\n" + c.Series[k].Label + ": " + usageNum(v))
		}
	}
	return b.String()
}

// renderAxis writes the tick labels as their own SVG, chartPadLeft wide and
// as tall as the plot. It uses the same scale as the bars.
func (c *usageChart) renderAxis() template.HTML {
	var b strings.Builder
	w, h := strconv.Itoa(chartPadLeft), strconv.Itoa(c.Height)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" class="usage-chart-svg usage-chart-axis" width="` + w + `" height="` + h +
		`" viewBox="0 0 ` + w + ` ` + h + `" aria-hidden="true" focusable="false"><g class="uc-ticks">`)
	for _, t := range c.Ticks {
		b.WriteString(`<text x="` + strconv.Itoa(chartPadLeft-8) + `" y="` + svgNum(c.y(t)) + `" dy="0.32em" text-anchor="end">` +
			compactTokens(t) + `</text>`)
	}
	b.WriteString(`</g></svg>`)
	return template.HTML(b.String())
}

// render writes the plot as SVG. Every text value passes through svgText,
// so the output is well-formed XML.
func (c *usageChart) render() template.HTML {
	var b strings.Builder
	w, h := strconv.Itoa(c.Width), strconv.Itoa(c.Height)
	base := svgNum(c.y(0))
	plotRight := svgNum(float64(chartInset + len(c.Days)*c.Slot))
	left := "0"

	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" class="usage-chart-svg" width="` + w + `" height="` + h +
		`" viewBox="0 0 ` + w + ` ` + h + `" role="img" aria-label="` + svgText(c.Label) + `">`)

	b.WriteString(`<g class="uc-grid">`)
	for _, t := range c.Ticks[1:] {
		y := svgNum(c.y(t))
		b.WriteString(`<line x1="` + left + `" x2="` + plotRight + `" y1="` + y + `" y2="` + y + `"/>`)
	}
	b.WriteString(`</g>`)

	barW := max(min(chartMaxBar, c.Slot*2/3), 4)
	for i, d := range c.Days {
		if d.Total <= 0 {
			continue
		}
		x0 := chartInset + i*c.Slot
		bx := float64(x0) + float64(c.Slot-barW)/2
		b.WriteString(`<g class="uc-bar"><title>` + svgText(c.describe(i)) + `</title>`)
		b.WriteString(`<rect class="uc-hit" x="` + strconv.Itoa(x0) + `" y="` + strconv.Itoa(chartPadTop) +
			`" width="` + strconv.Itoa(c.Slot) + `" height="` + strconv.Itoa(chartPlotH) + `"/>`)
		segs := c.segments(i)
		for j, s := range segs {
			class := c.Series[s.Series].Class
			height := s.H
			if j > 0 {
				// The gap comes off the bottom of every upper layer, so the
				// bar top stays at the day total.
				height -= chartGap
			}
			if height <= 0 {
				continue
			}
			if j == len(segs)-1 {
				b.WriteString(`<path class="` + class + `" d="` + roundedTop(bx, s.Y, float64(barW), height) + `"/>`)
				continue
			}
			b.WriteString(`<rect class="` + class + `" x="` + svgNum(bx) + `" y="` + svgNum(s.Y) +
				`" width="` + strconv.Itoa(barW) + `" height="` + svgNum(height) + `"/>`)
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`<line class="uc-axis" x1="` + left + `" x2="` + plotRight + `" y1="` + base + `" y2="` + base + `"/>`)

	// Day labels count back from the last day, so the latest day always has
	// one, and stay at least chartLabelGap apart.
	every := 91
	for _, e := range []int{1, 2, 7, 14, 28, 56, 91} {
		if e*c.Slot >= chartLabelGap {
			every = e
			break
		}
	}
	b.WriteString(`<g class="uc-days">`)
	ly := strconv.Itoa(chartPadTop + chartPlotH + 18)
	for i := len(c.Days) - 1; i >= 0; i -= every {
		cx := float64(chartInset+i*c.Slot) + float64(c.Slot)/2
		b.WriteString(`<text x="` + svgNum(cx) + `" y="` + ly + `" text-anchor="middle">` + c.Days[i].Date.Format("2 Jan") + `</text>`)
	}
	b.WriteString(`</g></svg>`)
	return template.HTML(b.String())
}

// roundedTop is the path of a bar layer with rounded top corners and a
// square bottom.
func roundedTop(x, y, w, h float64) string {
	r := min(chartRadius, w/2, h)
	bottom := y + h
	return "M" + svgNum(x) + "," + svgNum(bottom) +
		"V" + svgNum(y+r) +
		"Q" + svgNum(x) + "," + svgNum(y) + " " + svgNum(x+r) + "," + svgNum(y) +
		"H" + svgNum(x+w-r) +
		"Q" + svgNum(x+w) + "," + svgNum(y) + " " + svgNum(x+w) + "," + svgNum(y+r) +
		"V" + svgNum(bottom) + "Z"
}

// svgNum formats a coordinate with at most two decimals.
func svgNum(f float64) string {
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}

// svgText escapes text for an SVG element or attribute. It drops control
// characters, which XML does not allow, and keeps newlines for titles.
func svgText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		if r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, s)
	return template.HTMLEscapeString(s)
}

// usageDay returns the start of the UTC day of t.
func usageDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// usageMonth returns the start of the UTC month of t.
func usageMonth(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
