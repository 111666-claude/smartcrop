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
 */

package smartcrop

import (
	"errors"
	"fmt"
	"image"
	"math"
	"time"

	"golang.org/x/image/draw"
)

var (
	// ErrEmptySequence is returned when PlanCrops receives no frames.
	ErrEmptySequence = errors.New("smartcrop: empty frame sequence")

	// ErrTooManyFrames is returned when a sequence is longer than
	// SequenceOptions.MaxFrames. It bounds the memory used by the planner.
	ErrTooManyFrames = errors.New("smartcrop: frame sequence exceeds MaxFrames")

	// ErrNonMonotonicTime is returned when frame timestamps move backwards.
	// Equal timestamps are allowed and denote duplicated frames.
	ErrNonMonotonicTime = errors.New("smartcrop: frame timestamps move backwards")

	// ErrInvalidSequenceOptions is returned (possibly wrapped) when a
	// SequenceOptions field is NaN, infinite, out of range or inconsistent.
	ErrInvalidSequenceOptions = errors.New("smartcrop: invalid sequence options")

	// ErrInvalidFrame is returned when a frame has no image or empty bounds.
	ErrInvalidFrame = errors.New("smartcrop: invalid frame")
)

const (
	// DefaultSequenceMaxFrames is the default cap on the number of frames a
	// single PlanCrops call accepts.
	DefaultSequenceMaxFrames = 1024

	// HardSequenceMaxFrames is the largest MaxFrames value accepted. It is
	// the upper bound on the planner's trajectory buffers.
	HardSequenceMaxFrames = 1 << 17

	// DefaultSequenceAnalysisDimension is the default cap on the longest side
	// of the downscaled image used for salience analysis, in pixels.
	DefaultSequenceAnalysisDimension = 320

	hardSequenceAnalysisDimension = 1024

	defaultCropHeightFactor = 0.8
	defaultSalienceWeight   = 1.0
	defaultSmoothness       = 4.0
	defaultAccelWeight      = 16.0
	defaultOcclusionConf    = 0.2

	// unreliableFrameWeight is the relative data weight of a frame whose best
	// salience is far below the sequence maximum (e.g. an occluded subject).
	// The low weight lets the trajectory coast on motion continuity.
	unreliableFrameWeight = 0.05
)

// Frame is one entry in an ordered video sequence.
type Frame struct {
	// Image holds the frame pixels. Bounds may have a non-zero origin; crops
	// are returned in the frame's own coordinate system.
	Image image.Image

	// Time is the presentation timestamp. Timestamps must be non-decreasing
	// across a sequence; equal timestamps allow duplicated frames.
	Time time.Duration
}

// SequenceOptions configures PlanCrops.
//
// The zero value for most fields selects the package default. AspectRatio is
// required: PlanCrops rejects a zero, negative, NaN or infinite value.
type SequenceOptions struct {
	// AspectRatio is the target crop width divided by its height. Required.
	AspectRatio float64

	// CropHeightFactor is the crop height as a fraction of each frame's
	// height, in (0, 1]. Zero selects 0.8. The crop width follows from
	// AspectRatio and is clamped to the frame width, so a size change in the
	// sequence changes the crop size but keeps the aspect ratio.
	CropHeightFactor float64

	// SalienceWeight is the data weight that pulls a frame's crop towards its
	// salient region. Zero selects 1. Larger values make the window track the
	// subject more tightly; smaller values favour a stable trajectory.
	SalienceWeight float64

	// Smoothness scales the penalty on window displacement between
	// consecutive frames. Zero selects 4. Larger values produce slower,
	// smoother motion; smaller values allow the crop to follow per-frame
	// salience more closely.
	Smoothness float64

	// AccelerationWeight scales the penalty on changes of window velocity.
	// Zero selects 16. Larger values suppress abrupt starts and stops.
	AccelerationWeight float64

	// MaxStep is the maximum crop-centre displacement between two consecutive
	// frames, in pixels (Euclidean distance). Zero selects an automatic limit
	// of half the smaller crop dimension; a negative value disables the limit.
	MaxStep float64

	// MaxAcceleration is the maximum change of crop-centre velocity between
	// consecutive frames, in pixels per frame (Euclidean distance). Zero
	// selects an automatic limit of a quarter of the smaller crop dimension;
	// a negative value disables the limit.
	MaxAcceleration float64

	// OcclusionConfidence is the fraction of the sequence's strongest salience
	// below which a frame is treated as unreliable (for example, an occluded
	// subject). Zero selects 0.2; the value must be in [0, 1].
	OcclusionConfidence float64

	// MaxAnalysisDimension caps the longest side of the downscaled image used
	// for salience analysis, in pixels. Zero selects 320. Values below 16 or
	// above 1024 are rejected. This bounds per-frame working memory.
	MaxAnalysisDimension int

	// MaxFrames caps the sequence length. Zero selects
	// DefaultSequenceMaxFrames; values above HardSequenceMaxFrames are
	// rejected. This bounds the memory held by the trajectory planner.
	MaxFrames int
}

// DefaultSequenceOptions returns options for aspectRatio with the package
// defaults filled in explicitly.
func DefaultSequenceOptions(aspectRatio float64) SequenceOptions {
	return SequenceOptions{
		AspectRatio:          aspectRatio,
		CropHeightFactor:     defaultCropHeightFactor,
		SalienceWeight:       defaultSalienceWeight,
		Smoothness:           defaultSmoothness,
		AccelerationWeight:   defaultAccelWeight,
		OcclusionConfidence:  defaultOcclusionConf,
		MaxAnalysisDimension: DefaultSequenceAnalysisDimension,
		MaxFrames:            DefaultSequenceMaxFrames,
	}
}

// PlanCrops plans a temporally coherent crop for every frame in an ordered
// sequence.
//
// Each frame is analysed independently for salience, then the per-frame
// targets are smoothed into a single trajectory that trades subject salience
// against window displacement and acceleration. Slow subject motion yields a
// smoothly following window; sudden entrances, exits, occlusion and frame-size
// changes are absorbed by the velocity and acceleration limits.
//
// The returned rectangles use the same coordinate system as their frame's
// Bounds and always lie inside it. They have the requested aspect ratio (up to
// integer rounding) and a size derived from each frame's own dimensions.
//
// PlanCrops is deterministic: identical inputs and options produce identical
// output. It retains one downscaled salience buffer at a time and O(len(frames))
// small per-frame statistics; frame images are never copied into the result.
//
// It returns ErrEmptySequence for an empty slice (even with otherwise invalid
// options), ErrNonMonotonicTime if a timestamp decreases, ErrTooManyFrames if
// the sequence exceeds MaxFrames, ErrInvalidSequenceOptions for bad tuning
// values and ErrInvalidFrame for a nil or empty frame image.
func PlanCrops(frames []Frame, opts SequenceOptions) ([]image.Rectangle, error) {
	// The empty sequence has a single unambiguous result, independent of the
	// tuning values.
	if len(frames) == 0 {
		return nil, ErrEmptySequence
	}

	cfg, err := resolveSequenceOptions(opts)
	if err != nil {
		return nil, err
	}
	if len(frames) > cfg.maxFrames {
		return nil, fmt.Errorf("%w: got %d frames, limit %d",
			ErrTooManyFrames, len(frames), cfg.maxFrames)
	}
	for i := 1; i < len(frames); i++ {
		if frames[i].Time < frames[i-1].Time {
			return nil, fmt.Errorf("%w: frame %d (%v) precedes frame %d (%v)",
				ErrNonMonotonicTime, i, frames[i].Time, i-1, frames[i-1].Time)
		}
	}

	stats := make([]frameStat, len(frames))
	for i := range frames {
		stats[i], err = analyseSequenceFrame(frames[i], i, cfg)
		if err != nil {
			return nil, err
		}
	}

	assignSequenceTargets(stats, cfg)
	xs := solveTrajectoryAxis(stats, cfg, true)
	ys := solveTrajectoryAxis(stats, cfg, false)

	return applySequenceLimits(stats, xs, ys, cfg), nil
}

// sequenceConfig is the validated, defaulted form of SequenceOptions.
type sequenceConfig struct {
	aspectRatio      float64
	cropHeightFactor float64
	salienceWeight   float64
	smoothness       float64
	accelWeight      float64
	maxStep          float64
	maxAccel         float64
	occlusion        float64
	maxAnalysisDim   int
	maxFrames        int
}

func resolveSequenceOptions(opts SequenceOptions) (sequenceConfig, error) {
	cfg := sequenceConfig{
		aspectRatio:      opts.AspectRatio,
		cropHeightFactor: opts.CropHeightFactor,
		salienceWeight:   opts.SalienceWeight,
		smoothness:       opts.Smoothness,
		accelWeight:      opts.AccelerationWeight,
		maxStep:          opts.MaxStep,
		maxAccel:         opts.MaxAcceleration,
		occlusion:        opts.OcclusionConfidence,
		maxAnalysisDim:   opts.MaxAnalysisDimension,
		maxFrames:        opts.MaxFrames,
	}

	if math.IsNaN(cfg.aspectRatio) || math.IsInf(cfg.aspectRatio, 0) || cfg.aspectRatio <= 0 {
		return sequenceConfig{}, fmt.Errorf("%w: AspectRatio must be a finite positive number, got %v",
			ErrInvalidSequenceOptions, opts.AspectRatio)
	}

	var err error
	if cfg.cropHeightFactor, err = resolveUnitRange("CropHeightFactor", cfg.cropHeightFactor,
		defaultCropHeightFactor); err != nil {
		return sequenceConfig{}, err
	}
	if cfg.salienceWeight, err = resolveWeight("SalienceWeight", cfg.salienceWeight,
		defaultSalienceWeight); err != nil {
		return sequenceConfig{}, err
	}
	if cfg.smoothness, err = resolveWeight("Smoothness", cfg.smoothness,
		defaultSmoothness); err != nil {
		return sequenceConfig{}, err
	}
	if cfg.accelWeight, err = resolveWeight("AccelerationWeight", cfg.accelWeight,
		defaultAccelWeight); err != nil {
		return sequenceConfig{}, err
	}
	if math.IsNaN(cfg.maxStep) || math.IsInf(cfg.maxStep, 0) {
		return sequenceConfig{}, fmt.Errorf("%w: MaxStep must be finite, got %v",
			ErrInvalidSequenceOptions, opts.MaxStep)
	}
	if math.IsNaN(cfg.maxAccel) || math.IsInf(cfg.maxAccel, 0) {
		return sequenceConfig{}, fmt.Errorf("%w: MaxAcceleration must be finite, got %v",
			ErrInvalidSequenceOptions, opts.MaxAcceleration)
	}
	if math.IsNaN(cfg.occlusion) || math.IsInf(cfg.occlusion, 0) ||
		cfg.occlusion < 0 || cfg.occlusion > 1 {
		return sequenceConfig{}, fmt.Errorf("%w: OcclusionConfidence must be in [0, 1], got %v",
			ErrInvalidSequenceOptions, opts.OcclusionConfidence)
	}
	if cfg.occlusion == 0 {
		cfg.occlusion = defaultOcclusionConf
	}

	if cfg.maxAnalysisDim == 0 {
		cfg.maxAnalysisDim = DefaultSequenceAnalysisDimension
	}
	if cfg.maxAnalysisDim < 16 || cfg.maxAnalysisDim > hardSequenceAnalysisDimension {
		return sequenceConfig{}, fmt.Errorf("%w: MaxAnalysisDimension must be in [16, %d], got %d",
			ErrInvalidSequenceOptions, hardSequenceAnalysisDimension, opts.MaxAnalysisDimension)
	}
	if cfg.maxFrames == 0 {
		cfg.maxFrames = DefaultSequenceMaxFrames
	}
	if cfg.maxFrames < 0 || cfg.maxFrames > HardSequenceMaxFrames {
		return sequenceConfig{}, fmt.Errorf("%w: MaxFrames must be in [1, %d], got %d",
			ErrInvalidSequenceOptions, HardSequenceMaxFrames, opts.MaxFrames)
	}

	return cfg, nil
}

func resolveWeight(name string, value, fallback float64) (float64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("%w: %s must be a finite non-negative number, got %v",
			ErrInvalidSequenceOptions, name, value)
	}
	if value == 0 {
		return fallback, nil
	}
	return value, nil
}

// resolveUnitRange validates a value in (0, 1], substituting the fallback for
// the zero value.
func resolveUnitRange(name string, value, fallback float64) (float64, error) {
	if value == 0 {
		return fallback, nil
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return 0, fmt.Errorf("%w: %s must be in (0, 1], got %v",
			ErrInvalidSequenceOptions, name, value)
	}
	return value, nil
}

// frameStat is the small per-frame record retained by the planner. Full
// salience buffers are released as soon as a frame has been analysed.
type frameStat struct {
	bounds  image.Rectangle
	cropW   int
	cropH   int
	targetX float64 // absolute coordinates, in the frame's own space
	targetY float64
	dataW   float64 // relative data weight, 1 for reliable frames
	best    float64 // best salience score for the frame
}

// analyseSequenceFrame computes the static best crop for one frame.
func analyseSequenceFrame(frame Frame, index int, cfg sequenceConfig) (frameStat, error) {
	if frame.Image == nil {
		return frameStat{}, fmt.Errorf("%w: frame %d has a nil image", ErrInvalidFrame, index)
	}
	bounds := frame.Image.Bounds()
	if bounds.Empty() {
		return frameStat{}, fmt.Errorf("%w: frame %d has empty bounds", ErrInvalidFrame, index)
	}

	cropW, cropH := sequenceCropSize(bounds.Dx(), bounds.Dy(), cfg)

	features, scaleX, scaleY := sequenceSalience(frame.Image, cfg.maxAnalysisDim)
	analysisW := features.Bounds().Dx()
	analysisH := features.Bounds().Dy()

	acw := roundToIntClamped(float64(cropW)*scaleX, 1, analysisW)
	ach := roundToIntClamped(float64(cropH)*scaleY, 1, analysisH)

	stat := frameStat{
		bounds: bounds,
		cropW:  cropW,
		cropH:  cropH,
		best:   math.Inf(-1),
	}

	centeredX := float64(bounds.Min.X) + float64(bounds.Dx())/2
	centeredY := float64(bounds.Min.Y) + float64(bounds.Dy())/2
	stat.targetX = centeredX
	stat.targetY = centeredY

	bestX, bestY := analysisW/2, analysisH/2
	found := false
	for _, crop := range crops(features, float64(acw), float64(ach), 1.0) {
		crop.Score = score(features, crop)
		s := crop.totalScore()
		if !found || s > stat.best {
			found = true
			stat.best = s
			bestX = crop.Min.X + crop.Dx()/2
			bestY = crop.Min.Y + crop.Dy()/2
		}
	}
	if found {
		stat.targetX = float64(bounds.Min.X) + float64(bestX)/scaleX
		stat.targetY = float64(bounds.Min.Y) + float64(bestY)/scaleY
	}

	return stat, nil
}

// sequenceCropSize derives a per-frame crop size from the fixed aspect ratio.
// The crop height is a fraction of the frame height; the width follows from the
// aspect ratio and is clamped to the frame. When the requested ratio is wider
// than the frame, the height is refitted from the clamped width so the result
// stays as close to the requested ratio as integer pixels allow.
func sequenceCropSize(width, height int, cfg sequenceConfig) (int, int) {
	cropH := roundToIntClamped(float64(height)*cfg.cropHeightFactor, 1, height)
	cropW := roundToIntClamped(float64(cropH)*cfg.aspectRatio, 1, width)
	if cropW == width {
		cropH = roundToIntClamped(float64(cropW)/cfg.aspectRatio, 1, height)
	}
	return cropW, cropH
}

// sequenceSalience builds the downscaled feature image used by smartcrop's
// salience scoring. The destination always has a zero origin, so the existing
// detection code sees coordinates starting at (0, 0) regardless of the frame's
// own Bounds.
func sequenceSalience(img image.Image, maxDim int) (features *image.RGBA, scaleX, scaleY float64) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	scale := 1.0
	if longest := math.Max(float64(w), float64(h)); longest > float64(maxDim) {
		scale = float64(maxDim) / longest
	}
	aw := roundToIntClamped(float64(w)*scale, 1, w)
	ah := roundToIntClamped(float64(h)*scale, 1, h)

	small := image.NewRGBA(image.Rect(0, 0, aw, ah))
	draw.ApproxBiLinear.Scale(small, small.Bounds(), img, b, draw.Src, nil)

	features = image.NewRGBA(small.Bounds())
	edgeDetect(small, features)
	skinDetect(small, features)
	saturationDetect(small, features)

	return features, float64(aw) / float64(w), float64(ah) / float64(h)
}

// assignSequenceTargets normalises each frame's salience into a relative data
// weight and marks frames whose salience is too weak to trust (for example an
// occluded subject). Weak frames get their target from motion continuity: the
// last reliable target is extrapolated at its current velocity.
//
// Normalising by the strongest frame makes the trade-off independent of the
// absolute score scale, which smartcrop reports per crop area.
func assignSequenceTargets(stats []frameStat, cfg sequenceConfig) {
	maxBest := math.Inf(-1)
	for i := range stats {
		if stats[i].best > maxBest {
			maxBest = stats[i].best
		}
	}

	// A sequence without any positive salience (uniform frames) is planned
	// around each frame's centre rather than being treated as fully occluded.
	if !(maxBest > 0) {
		for i := range stats {
			stats[i].dataW = 1
			stats[i].targetX = frameCentreX(stats[i])
			stats[i].targetY = frameCentreY(stats[i])
		}
		return
	}

	var lastX, lastY, prevX, prevY float64
	count := 0

	for i := range stats {
		s := &stats[i]
		conf := s.best / maxBest
		reliable := s.best > 0 && conf >= cfg.occlusion
		if reliable {
			s.dataW = conf
			prevX, prevY = lastX, lastY
			lastX, lastY = s.targetX, s.targetY
			count++
			continue
		}

		s.dataW = unreliableFrameWeight
		switch {
		case count == 0:
			s.targetX = frameCentreX(*s)
			s.targetY = frameCentreY(*s)
		case count == 1:
			s.targetX = lastX
			s.targetY = lastY
		default:
			s.targetX = 2*lastX - prevX
			s.targetY = 2*lastY - prevY
		}

		prevX, prevY = lastX, lastY
		lastX, lastY = s.targetX, s.targetY
		count++
	}
}

func frameCentreX(s frameStat) float64 {
	return float64(s.bounds.Min.X) + float64(s.bounds.Dx())/2
}

func frameCentreY(s frameStat) float64 {
	return float64(s.bounds.Min.Y) + float64(s.bounds.Dy())/2
}

// solveTrajectoryAxis minimises
//
//	sum_t dataW[t]*(c[t]-target[t])^2
//	  + smoothness   * sum_t (c[t]-c[t-1])^2
//	  + accelWeight * sum_t (c[t]-2c[t-1]+c[t-2])^2
//
// for one axis. The normal equations are symmetric positive definite (every
// data weight is positive) with half-bandwidth 2, so they are solved exactly
// with a banded Cholesky factorisation: O(n) time and memory, no iteration
// count to tune, and a bit-for-bit deterministic result.
func solveTrajectoryAxis(stats []frameStat, cfg sequenceConfig, xAxis bool) []float64 {
	n := len(stats)
	targets := make([]float64, n)
	weights := make([]float64, n)
	rhs := make([]float64, n)
	for i := range stats {
		weights[i] = cfg.salienceWeight * stats[i].dataW
		if xAxis {
			targets[i] = stats[i].targetX
		} else {
			targets[i] = stats[i].targetY
		}
		rhs[i] = weights[i] * targets[i]
	}
	return solveBandedSPD(n, weights, rhs, cfg.smoothness, cfg.accelWeight)
}

// bandedSPD stores the lower triangle of a symmetric matrix with half
// bandwidth 2: diag[i] = A[i][i], lower1[i] = A[i+1][i], lower2[i] = A[i+2][i].
type bandedSPD struct {
	diag, lower1, lower2 []float64
}

// newBandedSystem builds the normal-equation matrix. The stencils are applied
// as quadratic forms so the small-n edge cases (missing first/last rows) fall
// out automatically.
func newBandedSystem(n int, weights []float64, lambdaVel, lambdaAcc float64) bandedSPD {
	m := bandedSPD{
		diag:   make([]float64, n),
		lower1: make([]float64, maxInt(n-1, 0)),
		lower2: make([]float64, maxInt(n-2, 0)),
	}
	copy(m.diag, weights)

	addStencil := func(centre int, coeffs []float64, weight float64) {
		for a := range coeffs {
			row := centre - a
			if row < 0 {
				continue
			}
			for b := range coeffs {
				col := centre - b
				if col < 0 {
					continue
				}
				v := weight * coeffs[a] * coeffs[b]
				switch {
				case row == col:
					m.diag[row] += v
				case row > col && row-col == 1:
					m.lower1[col] += v
				case row > col && row-col == 2:
					m.lower2[col] += v
				}
			}
		}
	}

	velocity := []float64{1, -1}
	acceleration := []float64{1, -2, 1}
	for i := 1; i < n; i++ {
		addStencil(i, velocity, lambdaVel)
	}
	for i := 2; i < n; i++ {
		addStencil(i, acceleration, lambdaAcc)
	}

	return m
}

func solveBandedSPD(n int, weights, rhs []float64, lambdaVel, lambdaAcc float64) []float64 {
	if n == 0 {
		return nil
	}
	m := newBandedSystem(n, weights, lambdaVel, lambdaAcc)

	// In-place banded Cholesky, A = L*L^T, reusing the band arrays.
	for j := 0; j < n; j++ {
		pivot := m.diag[j]
		if j >= 1 {
			pivot -= m.lower1[j-1] * m.lower1[j-1]
		}
		if j >= 2 {
			pivot -= m.lower2[j-2] * m.lower2[j-2]
		}
		if pivot <= 0 {
			// Mathematically impossible for positive data weights; guard
			// against underflow on pathological inputs.
			pivot = math.SmallestNonzeroFloat64
		}
		root := math.Sqrt(pivot)
		m.diag[j] = root

		if j+1 < n {
			v := m.lower1[j]
			if j >= 1 {
				v -= m.lower2[j-1] * m.lower1[j-1]
			}
			m.lower1[j] = v / root
		}
		if j+2 < n {
			m.lower2[j] /= root
		}
	}

	// Forward solve L*y = rhs (in place in rhs).
	for i := 0; i < n; i++ {
		v := rhs[i]
		if i >= 1 {
			v -= m.lower1[i-1] * rhs[i-1]
		}
		if i >= 2 {
			v -= m.lower2[i-2] * rhs[i-2]
		}
		rhs[i] = v / m.diag[i]
	}

	// Back solve L^T*c = y.
	for i := n - 1; i >= 0; i-- {
		v := rhs[i]
		if i+1 < n {
			v -= m.lower1[i] * rhs[i+1]
		}
		if i+2 < n {
			v -= m.lower2[i] * rhs[i+2]
		}
		rhs[i] = v / m.diag[i]
	}

	return rhs
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// applySequenceLimits turns the smoothed centres into rectangles while
// enforcing the hard motion limits. Bounds take precedence over motion limits:
// when a frame is smaller than the previous one, the crop must still lie inside
// it.
func applySequenceLimits(stats []frameStat, xs, ys []float64, cfg sequenceConfig) []image.Rectangle {
	n := len(stats)
	rects := make([]image.Rectangle, n)

	cx := clampFloat(xs[0], centreMinX(stats[0]), centreMaxX(stats[0]))
	cy := clampFloat(ys[0], centreMinY(stats[0]), centreMaxY(stats[0]))
	rects[0] = sequenceRect(stats[0], cx, cy)

	prevVX, prevVY := 0.0, 0.0
	for i := 1; i < n; i++ {
		dx := xs[i] - cx
		dy := ys[i] - cy

		maxAcc := sequenceAccelLimit(stats[i], cfg)
		if i >= 2 {
			dvx := dx - prevVX
			dvy := dy - prevVY
			if dist := math.Hypot(dvx, dvy); maxAcc > 0 && dist > maxAcc {
				k := maxAcc / dist
				dvx *= k
				dvy *= k
			}
			dx = prevVX + dvx
			dy = prevVY + dvy
		}

		maxStep := sequenceStepLimit(stats[i], cfg)
		if dist := math.Hypot(dx, dy); maxStep > 0 && dist > maxStep {
			k := maxStep / dist
			dx *= k
			dy *= k
		}

		nx := clampFloat(cx+dx, centreMinX(stats[i]), centreMaxX(stats[i]))
		ny := clampFloat(cy+dy, centreMinY(stats[i]), centreMaxY(stats[i]))

		prevVX = nx - cx
		prevVY = ny - cy
		cx, cy = nx, ny
		rects[i] = sequenceRect(stats[i], cx, cy)
	}

	return rects
}

func sequenceStepLimit(s frameStat, cfg sequenceConfig) float64 {
	switch {
	case cfg.maxStep < 0:
		return 0
	case cfg.maxStep > 0:
		return cfg.maxStep
	default:
		return math.Max(1, 0.5*float64(minInt(s.cropW, s.cropH)))
	}
}

func sequenceAccelLimit(s frameStat, cfg sequenceConfig) float64 {
	switch {
	case cfg.maxAccel < 0:
		return 0
	case cfg.maxAccel > 0:
		return cfg.maxAccel
	default:
		return math.Max(1, 0.25*float64(minInt(s.cropW, s.cropH)))
	}
}

func centreMinX(s frameStat) float64 {
	return float64(s.bounds.Min.X) + float64(s.cropW)/2
}

func centreMaxX(s frameStat) float64 {
	return float64(s.bounds.Max.X) - float64(s.cropW)/2
}

func centreMinY(s frameStat) float64 {
	return float64(s.bounds.Min.Y) + float64(s.cropH)/2
}

func centreMaxY(s frameStat) float64 {
	return float64(s.bounds.Max.Y) - float64(s.cropH)/2
}

func sequenceRect(s frameStat, cx, cy float64) image.Rectangle {
	x0 := roundToIntClamped(cx-float64(s.cropW)/2, s.bounds.Min.X, s.bounds.Max.X-s.cropW)
	y0 := roundToIntClamped(cy-float64(s.cropH)/2, s.bounds.Min.Y, s.bounds.Max.Y-s.cropH)
	return image.Rect(x0, y0, x0+s.cropW, y0+s.cropH)
}

func clampFloat(v, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if math.IsNaN(v) || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// roundToIntClamped rounds v to the nearest integer and clamps it to [lo, hi]
// without ever converting an out-of-range float to an int. Extreme but valid
// option values (a near-zero or enormous aspect ratio) can otherwise produce
// NaN or an overflowing product.
func roundToIntClamped(v float64, lo, hi int) int {
	if lo > hi {
		lo, hi = hi, lo
	}
	if math.IsNaN(v) || v <= float64(lo) {
		return lo
	}
	if v >= float64(hi) {
		return hi
	}
	return int(math.Round(v))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
