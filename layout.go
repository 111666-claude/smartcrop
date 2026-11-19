/*
 * Copyright (c) 2014-2017 Christian Muehlhaeuser
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 *
 *	Authors:
 *		Christian Muehlhaeuser <muesli@gmail.com>
 *		Michael Wendland <michael@michiwend.com>
 *		Bjørn Erik Pedersen <bjorn.erik.pedersen@gmail.com>
 */

package smartcrop

import (
	"errors"
	"image"
	"math"

	"github.com/muesli/smartcrop/options"
)

const (
	// DefaultMinScoreRetention is the minimum fraction of the best plain
	// crop's score a layout-aware crop must keep. It keeps the main subject
	// in the frame instead of running away to an empty part of the image.
	DefaultMinScoreRetention = 0.5

	// DefaultMaxTextPenalty is the weighted detail and skin coverage above
	// which a result is flagged as a fallback: the image did not offer enough
	// quiet space for the requested boxes.
	DefaultMaxTextPenalty = 0.2

	// DefaultMaxCandidates caps the number of evaluated crop x placement
	// combinations.
	DefaultMaxCandidates = 2000000

	// textDetailWeight and textSkinWeight are the per-pixel weights of the
	// detail (green) and skin (red) channels produced by smartcrop's
	// detection pass. Skin is weighted higher because a face under the copy
	// is the failure mode this feature exists to prevent.
	textDetailWeight = 1.0
	textSkinWeight   = 2.0

	// layoutPenaltyWeight trades text-box quietness against subject
	// significance in LayoutScore.
	layoutPenaltyWeight = 1.0

	maxLayoutTextBoxes  = 8
	maxLayoutPlacements = 10000
	maxLayoutAspect     = 20.0
)

// Layout planning errors.
var (
	// ErrNoTextBoxes is returned when no text boxes were supplied.
	ErrNoTextBoxes = errors.New("smartcrop: at least one text box is required")
	// ErrInvalidTextBox is returned for a text box with a non-positive size.
	ErrInvalidTextBox = errors.New("smartcrop: text box dimensions must be positive")
	// ErrInvalidAnchor is returned for an unknown text box anchor.
	ErrInvalidAnchor = errors.New("smartcrop: unknown text box anchor")
	// ErrInvalidLayoutOptions is returned for negative or out-of-range
	// options.
	ErrInvalidLayoutOptions = errors.New("smartcrop: invalid layout options")
	// ErrUnsupportedAspectRatio is returned for target aspect ratios beyond
	// maxLayoutAspect (or its reciprocal).
	ErrUnsupportedAspectRatio = errors.New("smartcrop: unsupported aspect ratio")
	// ErrCropLargerThanImage is returned when no crop with the requested
	// aspect ratio can be derived from the source image.
	ErrCropLargerThanImage = errors.New("smartcrop: requested output does not fit in the image")
	// ErrTextDoesNotFit is returned when a text box cannot fit inside the
	// crop's margins.
	ErrTextDoesNotFit = errors.New("smartcrop: text boxes do not fit inside the crop")
	// ErrNoValidPlacement is returned when no combination of anchors yields
	// non-overlapping, in-bounds text boxes.
	ErrNoValidPlacement = errors.New("smartcrop: no non-overlapping text box placement fits")
	// ErrTooManyCandidates is returned when the requested combination of
	// boxes, anchors and crop positions exceeds the candidate budget.
	ErrTooManyCandidates = errors.New("smartcrop: too many layout candidates")
)

// Anchor identifies a candidate position for a text box inside the crop.
// Anchors live in the coordinate space of the requested output
// (LayoutOptions.Width x LayoutOptions.Height), after LayoutOptions.Margin has
// been applied.
type Anchor int

// Candidate anchors. Top/middle/bottom are combined with left/centre/right.
const (
	AnchorTopLeft Anchor = iota
	AnchorTopCenter
	AnchorTopRight
	AnchorMiddleLeft
	AnchorMiddleCenter
	AnchorMiddleRight
	AnchorBottomLeft
	AnchorBottomCenter
	AnchorBottomRight
	anchorCount
)

// String returns a human readable anchor name.
func (a Anchor) String() string {
	switch a {
	case AnchorTopLeft:
		return "top-left"
	case AnchorTopCenter:
		return "top-center"
	case AnchorTopRight:
		return "top-right"
	case AnchorMiddleLeft:
		return "middle-left"
	case AnchorMiddleCenter:
		return "middle-center"
	case AnchorMiddleRight:
		return "middle-right"
	case AnchorBottomLeft:
		return "bottom-left"
	case AnchorBottomCenter:
		return "bottom-center"
	case AnchorBottomRight:
		return "bottom-right"
	default:
		return "unknown"
	}
}

func (a Anchor) valid() bool {
	return a >= AnchorTopLeft && a < anchorCount
}

// TextBox describes one block of copy that has to stay readable after
// cropping. Width and Height are measured in output pixels, i.e. in the same
// coordinate space as LayoutOptions.Width and LayoutOptions.Height.
type TextBox struct {
	Width  int
	Height int
}

// LayoutOptions configures FindBestCropWithLayout.
type LayoutOptions struct {
	// Width and Height define the target aspect ratio and the coordinate
	// space in which the text boxes are placed. Both must be positive.
	Width  int
	Height int

	// Boxes are the text boxes that must be placed. They are returned in the
	// same order as they were supplied.
	Boxes []TextBox

	// Anchors are the candidate anchors for each box. When empty the four
	// corners are used.
	Anchors []Anchor

	// Margin is the minimum distance between a text box and the crop edge.
	// Gap is the minimum distance between two text boxes.
	Margin int
	Gap    int

	// MinScoreRetention is the fraction of the best plain crop's score a
	// layout-aware crop must retain. Values <= 0 use
	// DefaultMinScoreRetention; values greater than 1 are rejected.
	MinScoreRetention float64

	// MaxTextPenalty is the weighted detail/skin coverage above which the
	// result is marked as a fallback. Values <= 0 use DefaultMaxTextPenalty.
	MaxTextPenalty float64

	// MaxCandidates caps the number of evaluated crop x placement
	// combinations. Values <= 0 use DefaultMaxCandidates.
	MaxCandidates int
}

// PlacedTextBox is a text box together with its final position. Rect is in
// output coordinates, relative to the top-left corner of Crop.
type PlacedTextBox struct {
	TextBox
	Anchor Anchor
	Rect   image.Rectangle
}

// CropLayout is the result of a layout-aware crop.
type CropLayout struct {
	// Crop is the selected rectangle in source image coordinates.
	Crop image.Rectangle

	// Boxes contains one entry per requested text box, in request order.
	Boxes []PlacedTextBox

	// Score holds the raw significance components of Crop.
	Score Score

	// BaseScore is the normalised significance score of Crop before the
	// text-box penalty is applied, in the same units as the plain
	// FindBestCrop score.
	BaseScore float64

	// BestBaseScore is the score of the best plain crop for the same aspect
	// ratio, i.e. the baseline the layout selection was measured against.
	BestBaseScore float64

	// TextPenalty is the area-weighted, normalised detail and skin coverage
	// underneath the text boxes, in [0, 1]. Lower is better.
	TextPenalty float64

	// LayoutScore is BaseScore/BestBaseScore -
	// layoutPenaltyWeight*TextPenalty, i.e. the normalised objective used to
	// select the crop. BestBaseScore is the score of the best plain crop for
	// the same aspect ratio.
	LayoutScore float64

	// Fallback is true when no candidate provided enough quiet space. The
	// result still has non-overlapping, in-bounds boxes.
	Fallback bool
}

type layoutPlacement struct {
	anchors []Anchor
	rects   []image.Rectangle
}

type scoredCrop struct {
	crop  Crop
	total float64
}

// LayoutAnalyzer is implemented by analyzers that can plan text-safe crops.
type LayoutAnalyzer interface {
	Analyzer
	FindBestCropWithLayout(img image.Image, opts LayoutOptions) (CropLayout, error)
}

// NewLayoutAnalyzer returns an analyzer that supports layout-aware crops.
func NewLayoutAnalyzer(resizer options.Resizer) LayoutAnalyzer {
	return NewAnalyzerWithLogger(resizer, Logger{}).(LayoutAnalyzer)
}

// NewLayoutAnalyzerWithLogger returns a layout analyzer with a logger.
func NewLayoutAnalyzerWithLogger(resizer options.Resizer, logger Logger) LayoutAnalyzer {
	return NewAnalyzerWithLogger(resizer, logger).(LayoutAnalyzer)
}

// FindBestCropWithLayout finds a crop that preserves smartcrop's normal
// significance while keeping the requested text boxes over quiet, non-skin
// areas.
//
// The crop is scored with the regular smartcrop pipeline; every candidate
// that retains at least MinScoreRetention of the best plain crop's score is
// then penalised by the detail and skin coverage underneath the boxes. The
// best joint candidate wins. Invalid input and impossible geometry return one
// of the package's deterministic errors; when the image has no sufficiently
// quiet area the quietest placement is returned with CropLayout.Fallback set.
//
// Transparent source pixels are composited over white. Images whose Bounds do
// not start at the origin are normalised internally, and the returned Crop is
// translated back into the source image's own coordinate space.
func (o smartcropAnalyzer) FindBestCropWithLayout(img image.Image, opts LayoutOptions) (CropLayout, error) {
	var result CropLayout

	opts, anchors, err := validateLayoutOptions(opts)
	if err != nil {
		return result, err
	}

	srcBounds := img.Bounds()
	if srcBounds.Empty() {
		return result, ErrInvalidDimensions
	}

	src := normalizeImage(img)
	srcWidth := src.Bounds().Dx()
	srcHeight := src.Bounds().Dy()
	if opts.Width > srcWidth && opts.Height > srcHeight {
		// Both output dimensions exceed the source: nothing would be
		// cropped, only upscaled.
		return result, ErrCropLargerThanImage
	}

	placements, err := buildLayoutPlacements(anchors, opts.Boxes, opts.Width, opts.Height, opts.Margin, opts.Gap, opts.MaxCandidates)
	if err != nil {
		return result, err
	}

	scale := math.Min(float64(srcWidth)/float64(opts.Width), float64(srcHeight)/float64(opts.Height))
	lowimg, prescalefactor := prepareLowImage(o.Resizer, src)

	cropWidth := chop(float64(opts.Width) * scale * prescalefactor)
	cropHeight := chop(float64(opts.Height) * scale * prescalefactor)
	if cropWidth < 1 || cropHeight < 1 {
		return result, ErrCropLargerThanImage
	}
	realMinScale := math.Min(maxScale, math.Max(1.0/scale, minScale))

	o.logger.Log.Printf("layout: original resolution: %dx%d\n", srcWidth, srcHeight)
	o.logger.Log.Printf("layout: scale: %f, cropw: %f, croph: %f, minscale: %f, placements: %d\n",
		scale, cropWidth, cropHeight, realMinScale, len(placements))

	detects := detectionImage(lowimg)
	scorer := newLayoutScorer(detects)

	maxCrops := opts.MaxCandidates / len(placements)
	if maxCrops < 1 {
		return result, ErrTooManyCandidates
	}
	crops, err := layoutCropCandidates(detects.Bounds(), cropWidth, cropHeight, realMinScale, maxCrops)
	if err != nil {
		return result, err
	}

	scored := make([]scoredCrop, len(crops))
	bestPlain := math.Inf(-1)
	bestPlainIndex := 0
	for i := range crops {
		crops[i].Score = score(detects, crops[i])
		total := crops[i].totalScore()
		scored[i] = scoredCrop{crop: crops[i], total: total}
		if total > bestPlain {
			bestPlain = total
			bestPlainIndex = i
		}
	}

	minBase := bestPlain * opts.MinScoreRetention
	if bestPlain <= 0 {
		// A non-positive baseline cannot be scaled meaningfully; keep every
		// crop so the quietest one can still win.
		minBase = math.Inf(-1)
	}

	type bestLayout struct {
		index     int
		placement int
		score     float64
		penalty   float64
	}
	best := bestLayout{index: -1, placement: -1, score: math.Inf(-1)}

	consider := func(index int, penalty float64, placement int) {
		baseNorm := 0.0
		if bestPlain > 0 {
			baseNorm = scored[index].total / bestPlain
		}
		layoutScore := baseNorm - layoutPenaltyWeight*penalty
		if layoutScore > best.score {
			best = bestLayout{
				index:     index,
				placement: placement,
				score:     layoutScore,
				penalty:   penalty,
			}
		}
	}

	for i := range scored {
		if scored[i].total < minBase {
			continue
		}
		penalty, placementIndex := scorer.bestPlacement(scored[i].crop.Rectangle, placements, opts.Width, opts.Height)
		if placementIndex < 0 {
			continue
		}
		consider(i, penalty, placementIndex)
	}

	if best.index < 0 {
		// The retention floor always admits the best plain crop; this branch
		// only guards against non-finite scores.
		penalty, placementIndex := scorer.bestPlacement(scored[bestPlainIndex].crop.Rectangle, placements, opts.Width, opts.Height)
		if placementIndex < 0 {
			return result, ErrNoValidPlacement
		}
		consider(bestPlainIndex, penalty, placementIndex)
	}

	chosen := scored[best.index]
	placement := placements[best.placement]

	result.Crop = layoutCropToSource(chosen.crop.Rectangle, prescalefactor, srcBounds)
	result.Score = chosen.crop.Score
	result.BaseScore = chosen.total
	result.BestBaseScore = bestPlain
	result.TextPenalty = best.penalty
	result.LayoutScore = best.score
	result.Fallback = best.penalty > opts.MaxTextPenalty
	result.Boxes = make([]PlacedTextBox, len(opts.Boxes))
	for i := range opts.Boxes {
		result.Boxes[i] = PlacedTextBox{
			TextBox: opts.Boxes[i],
			Anchor:  placement.anchors[i],
			Rect:    placement.rects[i],
		}
	}

	o.logger.Log.Printf("layout: crop=%v base=%.6f penalty=%.4f layout=%.4f fallback=%t\n",
		result.Crop, result.BaseScore, result.TextPenalty, result.LayoutScore, result.Fallback)

	return result, nil
}

func validateLayoutOptions(opts LayoutOptions) (LayoutOptions, []Anchor, error) {
	if opts.Width <= 0 || opts.Height <= 0 {
		return opts, nil, ErrInvalidDimensions
	}

	ratio := float64(opts.Width) / float64(opts.Height)
	if ratio > maxLayoutAspect || ratio < 1.0/maxLayoutAspect {
		return opts, nil, ErrUnsupportedAspectRatio
	}

	if len(opts.Boxes) == 0 {
		return opts, nil, ErrNoTextBoxes
	}
	if len(opts.Boxes) > maxLayoutTextBoxes {
		return opts, nil, ErrTooManyCandidates
	}
	for _, box := range opts.Boxes {
		if box.Width <= 0 || box.Height <= 0 {
			return opts, nil, ErrInvalidTextBox
		}
	}

	if opts.Margin < 0 || opts.Gap < 0 {
		return opts, nil, ErrInvalidLayoutOptions
	}
	if opts.MinScoreRetention > 1 {
		return opts, nil, ErrInvalidLayoutOptions
	}

	if opts.MinScoreRetention <= 0 {
		opts.MinScoreRetention = DefaultMinScoreRetention
	}
	if opts.MaxTextPenalty <= 0 {
		opts.MaxTextPenalty = DefaultMaxTextPenalty
	}
	if opts.MaxCandidates <= 0 {
		opts.MaxCandidates = DefaultMaxCandidates
	}

	anchors, err := normalizeAnchors(opts.Anchors)
	if err != nil {
		return opts, nil, err
	}

	return opts, anchors, nil
}

func normalizeAnchors(anchors []Anchor) ([]Anchor, error) {
	if len(anchors) == 0 {
		return []Anchor{AnchorTopLeft, AnchorTopRight, AnchorBottomLeft, AnchorBottomRight}, nil
	}

	seen := make(map[Anchor]bool, len(anchors))
	result := make([]Anchor, 0, len(anchors))
	for _, anchor := range anchors {
		if !anchor.valid() {
			return nil, ErrInvalidAnchor
		}
		if seen[anchor] {
			continue
		}
		seen[anchor] = true
		result = append(result, anchor)
	}

	return result, nil
}

// buildLayoutPlacements enumerates every anchor assignment in which the boxes
// stay inside the margins and keep at least Gap pixels between them.
func buildLayoutPlacements(anchors []Anchor, boxes []TextBox, width, height, margin, gap, maxCandidates int) ([]layoutPlacement, error) {
	inner := image.Rect(margin, margin, width-margin, height-margin)
	if inner.Dx() <= 0 || inner.Dy() <= 0 {
		return nil, ErrTextDoesNotFit
	}
	for _, box := range boxes {
		if box.Width > inner.Dx() || box.Height > inner.Dy() {
			return nil, ErrTextDoesNotFit
		}
	}

	combinations := 1
	for range boxes {
		if combinations > maxCandidates/len(anchors) {
			return nil, ErrTooManyCandidates
		}
		combinations *= len(anchors)
	}
	if combinations > maxLayoutPlacements {
		return nil, ErrTooManyCandidates
	}

	placements := make([]layoutPlacement, 0, combinations)
	rects := make([]image.Rectangle, len(boxes))
	chosen := make([]Anchor, len(boxes))

	var walk func(int)
	walk = func(index int) {
		if index == len(boxes) {
			placement := layoutPlacement{
				anchors: make([]Anchor, len(boxes)),
				rects:   make([]image.Rectangle, len(boxes)),
			}
			copy(placement.anchors, chosen)
			copy(placement.rects, rects)
			placements = append(placements, placement)
			return
		}

		for _, anchor := range anchors {
			rect := anchorRectangle(anchor, boxes[index], inner)
			conflict := false
			for i := 0; i < index; i++ {
				if layoutRectsConflict(rects[i], rect, gap) {
					conflict = true
					break
				}
			}
			if conflict {
				continue
			}
			rects[index] = rect
			chosen[index] = anchor
			walk(index + 1)
		}
	}

	walk(0)
	if len(placements) == 0 {
		return nil, ErrNoValidPlacement
	}

	return placements, nil
}

func anchorRectangle(anchor Anchor, box TextBox, inner image.Rectangle) image.Rectangle {
	horizontal := int(anchor) % 3
	vertical := int(anchor) / 3

	x := inner.Min.X
	switch horizontal {
	case 1:
		x = inner.Min.X + (inner.Dx()-box.Width)/2
	case 2:
		x = inner.Max.X - box.Width
	}

	y := inner.Min.Y
	switch vertical {
	case 1:
		y = inner.Min.Y + (inner.Dy()-box.Height)/2
	case 2:
		y = inner.Max.Y - box.Height
	}

	return image.Rect(x, y, x+box.Width, y+box.Height)
}

// layoutRectsConflict reports whether a and b overlap or are closer than gap.
func layoutRectsConflict(a, b image.Rectangle, gap int) bool {
	return a.Min.X-gap < b.Max.X && b.Min.X-gap < a.Max.X &&
		a.Min.Y-gap < b.Max.Y && b.Min.Y-gap < a.Max.Y
}

func layoutCropCandidates(bounds image.Rectangle, cropWidth, cropHeight, realMinScale float64, limit int) ([]Crop, error) {
	capacity := limit
	if capacity > 4096 {
		capacity = 4096
	}
	crops := make([]Crop, 0, capacity)
	width := bounds.Dx()
	height := bounds.Dy()

	minDimension := math.Min(float64(width), float64(height))
	if cropWidth == 0.0 {
		cropWidth = minDimension
	}
	if cropHeight == 0.0 {
		cropHeight = minDimension
	}

	for scale := maxScale; scale >= realMinScale; scale -= scaleStep {
		for y := 0; float64(y)+cropHeight*scale <= float64(height); y += step {
			for x := 0; float64(x)+cropWidth*scale <= float64(width); x += step {
				if len(crops) >= limit {
					return nil, ErrTooManyCandidates
				}
				crops = append(crops, Crop{
					Rectangle: image.Rect(x, y, x+int(cropWidth*scale), y+int(cropHeight*scale)),
				})
			}
		}
	}

	if len(crops) == 0 {
		return nil, ErrCropLargerThanImage
	}

	return crops, nil
}

// layoutScorer answers text-box coverage queries in constant time using
// summed-area tables over the same detail (green) and skin (red) channels that
// smartcrop's significance score uses.
type layoutScorer struct {
	bounds image.Rectangle
	stride int
	sum    []float64
}

func newLayoutScorer(detects *image.RGBA) *layoutScorer {
	bounds := detects.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	stride := width + 1

	s := &layoutScorer{
		bounds: bounds,
		stride: stride,
		sum:    make([]float64, stride*(height+1)),
	}

	for y := 0; y < height; y++ {
		var rowSum float64
		for x := 0; x < width; x++ {
			c := detects.RGBAAt(bounds.Min.X+x, bounds.Min.Y+y)
			rowSum += textDetailWeight*float64(c.G)/255.0 + textSkinWeight*float64(c.R)/255.0
			s.sum[(y+1)*stride+x+1] = s.sum[y*stride+x+1] + rowSum
		}
	}

	return s
}

// meanCoverage returns the average weighted detail/skin coverage of rect in
// [0, 1], clamped to the detection image.
func (s *layoutScorer) meanCoverage(rect image.Rectangle) float64 {
	rect = rect.Intersect(s.bounds)
	if rect.Empty() {
		return 0
	}

	stride := s.stride
	x0, y0 := rect.Min.X-s.bounds.Min.X, rect.Min.Y-s.bounds.Min.Y
	x1, y1 := rect.Max.X-s.bounds.Min.X, rect.Max.Y-s.bounds.Min.Y

	total := s.sum[y1*stride+x1] - s.sum[y0*stride+x1] - s.sum[y1*stride+x0] + s.sum[y0*stride+x0]
	// Floating point cancellation in the summed-area table can produce tiny
	// negative values; coverage is non-negative by construction.
	coverage := total / float64(rect.Dx()) / float64(rect.Dy()) / (textDetailWeight + textSkinWeight)
	return math.Max(coverage, 0)
}

// bestPlacement returns the placement with the lowest text coverage for crop,
// and its index. It returns -1 when there is no placement.
func (s *layoutScorer) bestPlacement(crop image.Rectangle, placements []layoutPlacement, outWidth, outHeight int) (float64, int) {
	bestPenalty := math.Inf(1)
	bestIndex := -1

	for i := range placements {
		penalty := s.placementPenalty(crop, placements[i].rects, outWidth, outHeight)
		if penalty < bestPenalty {
			bestPenalty = penalty
			bestIndex = i
		}
	}

	if bestIndex < 0 {
		return 0, -1
	}
	return bestPenalty, bestIndex
}

func (s *layoutScorer) placementPenalty(crop image.Rectangle, rects []image.Rectangle, outWidth, outHeight int) float64 {
	var weighted, area float64
	for _, rect := range rects {
		region := mapOutputRectToCrop(rect, crop, outWidth, outHeight)
		weight := float64(region.Dx() * region.Dy())
		weighted += weight * s.meanCoverage(region)
		area += weight
	}
	if area == 0 {
		return 0
	}
	return weighted / area
}

func mapOutputRectToCrop(textRect image.Rectangle, crop image.Rectangle, outWidth, outHeight int) image.Rectangle {
	scaleX := float64(crop.Dx()) / float64(outWidth)
	scaleY := float64(crop.Dy()) / float64(outHeight)

	x0 := crop.Min.X + int(math.Floor(float64(textRect.Min.X)*scaleX))
	y0 := crop.Min.Y + int(math.Floor(float64(textRect.Min.Y)*scaleY))
	x1 := crop.Min.X + int(math.Ceil(float64(textRect.Max.X)*scaleX))
	y1 := crop.Min.Y + int(math.Ceil(float64(textRect.Max.Y)*scaleY))

	x0 = clampInt(x0, crop.Min.X, crop.Max.X-1)
	y0 = clampInt(y0, crop.Min.Y, crop.Max.Y-1)
	x1 = clampInt(x1, x0+1, crop.Max.X)
	y1 = clampInt(y1, y0+1, crop.Max.Y)

	return image.Rect(x0, y0, x1, y1)
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// detectionImage builds the channel image used for significance scoring:
// green is edge detail, red is skin and blue is saturation.
func detectionImage(img *image.RGBA) *image.RGBA {
	o := image.NewRGBA(img.Bounds())
	edgeDetect(img, o)
	skinDetect(img, o)
	saturationDetect(img, o)
	return o
}

// prepareLowImage reproduces the prescaling step shared by FindBestCrop and
// FindBestCropWithLayout. It returns the normalised low-resolution image used
// for detection and the factor needed to map crop coordinates back to the
// source image.
func prepareLowImage(resizer options.Resizer, src *image.RGBA) (*image.RGBA, float64) {
	if !prescale {
		return src, 1.0
	}

	minDimension := math.Min(float64(src.Bounds().Dx()), float64(src.Bounds().Dy()))
	prescalefactor := 1.0
	if f := prescaleMin / minDimension; f < 1.0 {
		prescalefactor = f
	}
	if prescalefactor == 1.0 {
		return src, 1.0
	}

	smallimg := resizer.Resize(src, uint(float64(src.Bounds().Dx())*prescalefactor), 0)
	lowimg := normalizeImage(smallimg)

	return lowimg, prescalefactor
}

func layoutCropToSource(crop image.Rectangle, prescalefactor float64, srcBounds image.Rectangle) image.Rectangle {
	result := image.Rect(
		int(chop(float64(crop.Min.X)/prescalefactor)),
		int(chop(float64(crop.Min.Y)/prescalefactor)),
		int(chop(float64(crop.Max.X)/prescalefactor)),
		int(chop(float64(crop.Max.Y)/prescalefactor)),
	).Canon()

	result = result.Add(srcBounds.Min).Intersect(srcBounds)
	return result.Canon()
}
