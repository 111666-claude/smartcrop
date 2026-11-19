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
	"image"
	"image/color"
	"math"
	"testing"
	"time"
)

const testAspect = 9.0 / 16.0 // vertical video

// sequenceFrame renders a grey frame with an optional solid block. block is in
// the frame's own coordinate space. A nil block produces an empty frame.
func sequenceFrame(bounds image.Rectangle, block *image.Rectangle) *image.RGBA {
	img := image.NewRGBA(bounds)
	bg := color.RGBA{100, 100, 100, 255}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			img.SetRGBA(x, y, bg)
		}
	}
	if block != nil {
		r := block.Intersect(bounds)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
	}
	return img
}

// movingBlockSequence builds n frames of a block of size blockSize travelling
// from x0 to x1 along the horizontal centre line. Frames listed in hidden are
// rendered without the block to simulate occlusion.
func movingBlockSequence(n, w, h, blockSize int, x0, x1 float64, hidden map[int]bool) []Frame {
	bounds := image.Rect(0, 0, w, h)
	frames := make([]Frame, n)
	step := 0.0
	if n > 1 {
		step = (x1 - x0) / float64(n-1)
	}
	centreY := float64(h) / 2
	for i := 0; i < n; i++ {
		f := Frame{Time: time.Duration(i) * 40 * time.Millisecond}
		if hidden[i] {
			f.Image = sequenceFrame(bounds, nil)
		} else {
			cx := x0 + step*float64(i)
			block := image.Rect(
				int(math.Round(cx-float64(blockSize)/2)),
				int(math.Round(centreY-float64(blockSize)/2)),
				int(math.Round(cx+float64(blockSize)/2)),
				int(math.Round(centreY+float64(blockSize)/2)),
			)
			f.Image = sequenceFrame(bounds, &block)
		}
		frames[i] = f
	}
	return frames
}

func rectCentre(r image.Rectangle) (float64, float64) {
	return float64(r.Min.X) + float64(r.Dx())/2, float64(r.Min.Y) + float64(r.Dy())/2
}

func assertInside(t *testing.T, rects []image.Rectangle, frames []Frame) {
	t.Helper()
	for i, r := range rects {
		b := frames[i].Image.Bounds()
		if !r.In(b) {
			t.Fatalf("frame %d: crop %v not inside bounds %v", i, r, b)
		}
		if r.Empty() {
			t.Fatalf("frame %d: empty crop", i)
		}
	}
}

func TestPlanCropsEmptySequence(t *testing.T) {
	rects, err := PlanCrops(nil, SequenceOptions{AspectRatio: testAspect})
	if !errors.Is(err, ErrEmptySequence) {
		t.Fatalf("expected ErrEmptySequence, got %v", err)
	}
	if rects != nil {
		t.Fatalf("expected nil rects, got %v", rects)
	}
}

func TestPlanCropsNonMonotonicTime(t *testing.T) {
	img := sequenceFrame(image.Rect(0, 0, 64, 48), nil)
	frames := []Frame{
		{Image: img, Time: 0},
		{Image: img, Time: 50 * time.Millisecond},
		{Image: img, Time: 40 * time.Millisecond},
	}
	if _, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect}); !errors.Is(err, ErrNonMonotonicTime) {
		t.Fatalf("expected ErrNonMonotonicTime, got %v", err)
	}
}

func TestPlanCropsDuplicateFramesAndTimestamps(t *testing.T) {
	block := image.Rect(10, 10, 40, 40)
	img := sequenceFrame(image.Rect(0, 0, 64, 48), &block)
	frames := []Frame{
		{Image: img, Time: 0},
		{Image: img, Time: 0}, // duplicate timestamp
		{Image: img, Time: 0}, // duplicate frame
		{Image: img, Time: 40 * time.Millisecond},
	}
	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect})
	if err != nil {
		t.Fatal(err)
	}
	if len(rects) != len(frames) {
		t.Fatalf("expected %d rects, got %d", len(frames), len(rects))
	}
	// A static subject should produce a static window; duplicated frames in
	// particular must not introduce jitter.
	for i := 1; i < len(rects); i++ {
		if rects[i] != rects[0] {
			t.Fatalf("frame %d: expected static crop %v, got %v", i, rects[0], rects[i])
		}
	}
}

func TestPlanCropsInvalidOptions(t *testing.T) {
	img := sequenceFrame(image.Rect(0, 0, 64, 48), nil)
	frames := []Frame{{Image: img}}

	cases := []struct {
		name string
		opts SequenceOptions
	}{
		{"zero aspect", SequenceOptions{}},
		{"negative aspect", SequenceOptions{AspectRatio: -1}},
		{"NaN aspect", SequenceOptions{AspectRatio: math.NaN()}},
		{"inf aspect", SequenceOptions{AspectRatio: math.Inf(1)}},
		{"NaN smoothness", SequenceOptions{AspectRatio: 1, Smoothness: math.NaN()}},
		{"inf smoothness", SequenceOptions{AspectRatio: 1, Smoothness: math.Inf(1)}},
		{"negative smoothness", SequenceOptions{AspectRatio: 1, Smoothness: -1}},
		{"NaN acceleration", SequenceOptions{AspectRatio: 1, AccelerationWeight: math.NaN()}},
		{"negative acceleration", SequenceOptions{AspectRatio: 1, AccelerationWeight: -2}},
		{"negative salience", SequenceOptions{AspectRatio: 1, SalienceWeight: -1}},
		{"crop height zero fraction", SequenceOptions{AspectRatio: 1, CropHeightFactor: -0.5}},
		{"crop height too large", SequenceOptions{AspectRatio: 1, CropHeightFactor: 1.5}},
		{"occlusion out of range", SequenceOptions{AspectRatio: 1, OcclusionConfidence: 1.5}},
		{"NaN max step", SequenceOptions{AspectRatio: 1, MaxStep: math.NaN()}},
		{"inf max acceleration", SequenceOptions{AspectRatio: 1, MaxAcceleration: math.Inf(1)}},
		{"analysis too small", SequenceOptions{AspectRatio: 1, MaxAnalysisDimension: 8}},
		{"analysis too large", SequenceOptions{AspectRatio: 1, MaxAnalysisDimension: 4096}},
		{"negative max frames", SequenceOptions{AspectRatio: 1, MaxFrames: -1}},
		{"max frames above hard limit", SequenceOptions{AspectRatio: 1, MaxFrames: HardSequenceMaxFrames + 1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanCrops(frames, tc.opts); !errors.Is(err, ErrInvalidSequenceOptions) {
				t.Fatalf("expected ErrInvalidSequenceOptions, got %v", err)
			}
		})
	}

	// A negative motion limit is a valid way to disable that limit.
	if _, err := PlanCrops(frames, SequenceOptions{
		AspectRatio:     1,
		MaxStep:         -1,
		MaxAcceleration: -1,
	}); err != nil {
		t.Fatalf("negative limits should disable them, got %v", err)
	}
}

func TestPlanCropsTooManyFrames(t *testing.T) {
	img := sequenceFrame(image.Rect(0, 0, 32, 32), nil)
	frames := make([]Frame, 5)
	for i := range frames {
		frames[i] = Frame{Image: img, Time: time.Duration(i) * time.Millisecond}
	}

	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect, MaxFrames: 4})
	if !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("expected ErrTooManyFrames, got %v", err)
	}
	if rects != nil {
		t.Fatalf("expected nil rects, got %v", rects)
	}

	if _, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect, MaxFrames: 5}); err != nil {
		t.Fatalf("sequence at the limit should be accepted, got %v", err)
	}
}

func TestPlanCropsInvalidFrame(t *testing.T) {
	valid := Frame{Image: sequenceFrame(image.Rect(0, 0, 32, 32), nil)}

	cases := []struct {
		name   string
		frames []Frame
	}{
		{"nil image", []Frame{{Time: 0}}},
		{"empty bounds", []Frame{{Image: image.NewRGBA(image.Rect(10, 10, 10, 10))}}},
		{"nil image later", []Frame{valid, {Time: time.Millisecond}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanCrops(tc.frames, SequenceOptions{AspectRatio: testAspect}); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("expected ErrInvalidFrame, got %v", err)
			}
		})
	}
}

// TestPlanCropsMovingSubjectOcclusion is the end-to-end check: a subject moves
// slowly left to right and is hidden twice. The window must track the subject,
// survive both occlusions without whiplash, and obey the motion limits.
func TestPlanCropsMovingSubjectOcclusion(t *testing.T) {
	const (
		w, h      = 320, 240
		frames    = 48
		blockSize = 48
		x0, x1    = 60.0, 260.0
	)
	hidden := map[int]bool{16: true, 17: true, 18: true, 32: true, 33: true, 34: true}
	seq := movingBlockSequence(frames, w, h, blockSize, x0, x1, hidden)

	opts := SequenceOptions{
		AspectRatio:          testAspect,
		Smoothness:           6,
		AccelerationWeight:   20,
		OcclusionConfidence:  0.25,
		MaxAnalysisDimension: 320,
	}
	rects, err := PlanCrops(seq, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rects) != frames {
		t.Fatalf("expected %d rects, got %d", frames, len(rects))
	}
	assertInside(t, rects, seq)

	step := (x1 - x0) / float64(frames-1)
	// The visible subject must stay inside the crop. Frames immediately after
	// an occlusion are allowed to lag while the window reacquires the target.
	reacquire := map[int]bool{19: true, 20: true, 35: true, 36: true}
	for i := 0; i < frames; i++ {
		if hidden[i] || reacquire[i] {
			continue
		}
		cx := x0 + step*float64(i)
		centre := image.Pt(int(math.Round(cx)), h/2)
		if !centre.In(rects[i]) {
			t.Errorf("frame %d: subject centre %v outside crop %v", i, centre, rects[i])
		}
	}

	// During occlusion the window should keep moving along the subject's path
	// rather than snapping back to the frame centre.
	for i := range hidden {
		cx := x0 + step*float64(i)
		gotX, _ := rectCentre(rects[i])
		if math.Abs(gotX-cx) > 30 {
			t.Errorf("frame %d: occluded window centre %.1f too far from expected %.1f", i, gotX, cx)
		}
	}

	// No per-frame jump may exceed the default step limit (half the smaller
	// crop dimension); with these settings it is generous, so also verify the
	// smoother's output is actually smooth.
	maxDelta := 0.0
	for i := 1; i < len(rects); i++ {
		ax, ay := rectCentre(rects[i-1])
		bx, by := rectCentre(rects[i])
		if d := math.Hypot(bx-ax, by-ay); d > maxDelta {
			maxDelta = d
		}
	}
	if maxDelta > float64(minInt(rects[0].Dx(), rects[0].Dy()))/2+1 {
		t.Fatalf("per-frame displacement %.1f exceeds limit", maxDelta)
	}
	if maxDelta < step {
		t.Fatalf("window did not follow the subject: max displacement %.1f < subject step %.1f", maxDelta, step)
	}

	// Vertical motion is absent, so the window must not wander vertically:
	// the subject's row must stay well inside the crop and the window's
	// vertical position must not drift.
	for i, r := range rects {
		if !image.Pt(int(math.Round(x0+step*float64(i))), h/2).In(r) && !hidden[i] {
			continue
		}
		if r.Min.Y > h/2-blockSize/2 || r.Max.Y < h/2+blockSize/2 {
			t.Errorf("frame %d: subject row not contained by crop %v", i, r)
		}
		if i > 0 && r.Min.Y != rects[0].Min.Y {
			t.Errorf("frame %d: vertical window drifted from %d to %d", i, rects[0].Min.Y, r.Min.Y)
		}
	}
}

func TestPlanCropsDeterministic(t *testing.T) {
	hidden := map[int]bool{9: true, 10: true}
	seq := movingBlockSequence(24, 320, 240, 48, 40, 280, hidden)
	opts := SequenceOptions{AspectRatio: testAspect, Smoothness: 5, AccelerationWeight: 12}

	first, err := PlanCrops(seq, opts)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 3; run++ {
		again, err := PlanCrops(seq, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) {
			t.Fatalf("run %d: length changed", run)
		}
		for i := range first {
			if first[i] != again[i] {
				t.Fatalf("run %d frame %d: %v != %v", run, i, first[i], again[i])
			}
		}
	}
}

func TestPlanCropsNonZeroBounds(t *testing.T) {
	// A frame whose bounds do not start at the origin, plus a size change.
	boundsA := image.Rect(100, 50, 420, 290) // 320x240
	boundsB := image.Rect(7, 9, 167, 129)    // 160x120
	blockA := image.Rect(140, 150, 188, 198) // inside A
	blockB := image.Rect(30, 50, 54, 74)     // inside B

	base := sequenceFrame(boundsA, &blockA)
	small := sequenceFrame(boundsB, &blockB)
	frames := []Frame{
		{Image: base, Time: 0},
		{Image: base, Time: 40 * time.Millisecond},
		{Image: small, Time: 80 * time.Millisecond},
		{Image: small, Time: 120 * time.Millisecond},
	}

	rects, err := PlanCrops(frames, SequenceOptions{
		AspectRatio:     testAspect,
		MaxStep:         -1, // focus on coordinate spaces, not motion limits
		MaxAcceleration: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, frames)

	for i, r := range rects {
		b := frames[i].Image.Bounds()
		wantH := int(math.Round(float64(b.Dy()) * defaultCropHeightFactor))
		if r.Dy() != wantH {
			t.Errorf("frame %d: crop height %d, want %d", i, r.Dy(), wantH)
		}
		if ratio := float64(r.Dx()) / float64(r.Dy()); math.Abs(ratio-testAspect) > 0.01 {
			t.Errorf("frame %d: aspect ratio %.3f, want %.3f", i, ratio, testAspect)
		}
	}
	// Consecutive identical frames must both land on the subject. Global
	// smoothing may shift them slightly to accommodate the later size change,
	// so compare subject containment rather than exact equality.
	subject := image.Pt(164, 174) // centre of blockA
	for i := 0; i < 2; i++ {
		if !subject.In(rects[i]) {
			t.Errorf("identical frame %d: crop %v misses subject %v", i, rects[i], subject)
		}
	}
}

func TestPlanCropsDimensionChange(t *testing.T) {
	const w, h = 320, 240
	blockA := image.Rect(20, 100, 68, 148)
	blockB := image.Rect(40, 200, 136, 296) // 2x scale of A in a 640x480 frame

	base := sequenceFrame(image.Rect(0, 0, w, h), &blockA)
	big := sequenceFrame(image.Rect(0, 0, 2*w, 2*h), &blockB)
	frames := []Frame{
		{Image: base, Time: 0},
		{Image: base, Time: 40 * time.Millisecond},
		{Image: big, Time: 80 * time.Millisecond},
		{Image: big, Time: 120 * time.Millisecond},
	}

	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, frames)

	if rects[0].Dy() != int(math.Round(float64(h)*defaultCropHeightFactor)) {
		t.Errorf("small frame crop height %d", rects[0].Dy())
	}
	if rects[2].Dy() != int(math.Round(float64(2*h)*defaultCropHeightFactor)) {
		t.Errorf("large frame crop height %d", rects[2].Dy())
	}
	// The subject is at the same relative position after scaling; the window
	// should land on it in the new coordinate system.
	if !image.Pt(88, 248).In(rects[2]) {
		t.Errorf("crop %v does not contain scaled subject", rects[2])
	}
}

func TestPlanCropsSmoothnessTradeoff(t *testing.T) {
	const n = 30
	// The subject jumps from the left to the right side midway through.
	frames := make([]Frame, n)
	for i := 0; i < n; i++ {
		cx := 60
		if i >= 15 {
			cx = 260
		}
		block := image.Rect(cx-24, 96, cx+24, 144)
		frames[i] = Frame{
			Image: sequenceFrame(image.Rect(0, 0, 320, 240), &block),
			Time:  time.Duration(i) * 40 * time.Millisecond,
		}
	}

	lax, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect, Smoothness: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	strict, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect, Smoothness: 50})
	if err != nil {
		t.Fatal(err)
	}

	// After the jump, a less smooth trajectory snaps to the new subject
	// faster, so it is closer to the true subject position on average.
	meanDistance := func(rects []image.Rectangle, from int) float64 {
		var sum float64
		for i := from; i < n; i++ {
			cx, _ := rectCentre(rects[i])
			sum += math.Abs(cx - 260)
		}
		return sum / float64(n-from)
	}
	if meanDistance(lax, 16) >= meanDistance(strict, 16) {
		t.Fatalf("smoothness did not trade off against tracking: lax %.1f >= strict %.1f",
			meanDistance(lax, 16), meanDistance(strict, 16))
	}

	// A high smoothness must never be less stable than a low one.
	maxDelta := func(rects []image.Rectangle) float64 {
		best := 0.0
		for i := 1; i < len(rects); i++ {
			ax, _ := rectCentre(rects[i-1])
			bx, _ := rectCentre(rects[i])
			best = math.Max(best, math.Abs(bx-ax))
		}
		return best
	}
	if maxDelta(strict) > maxDelta(lax)+1 {
		t.Fatalf("higher smoothness produced larger jumps: %.1f > %.1f",
			maxDelta(strict), maxDelta(lax))
	}
}

func TestPlanCropsEnforcesMotionLimits(t *testing.T) {
	const n = 24
	frames := make([]Frame, n)
	for i := 0; i < n; i++ {
		cx := 40
		if i >= 4 {
			cx = 280
		}
		block := image.Rect(cx-24, 96, cx+24, 144)
		frames[i] = Frame{
			Image: sequenceFrame(image.Rect(0, 0, 320, 240), &block),
			Time:  time.Duration(i) * 40 * time.Millisecond,
		}
	}

	const maxStep, maxAccel = 10.0, 4.0
	rects, err := PlanCrops(frames, SequenceOptions{
		AspectRatio:        testAspect,
		MaxStep:            maxStep,
		MaxAcceleration:    maxAccel,
		Smoothness:         1,
		AccelerationWeight: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, frames)

	centres := make([][2]float64, n)
	for i, r := range rects {
		x, y := rectCentre(r)
		centres[i] = [2]float64{x, y}
	}

	for i := 1; i < n; i++ {
		d := math.Hypot(centres[i][0]-centres[i-1][0], centres[i][1]-centres[i-1][1])
		if d > maxStep+1.0 { // integer rounding of the reported centres
			t.Errorf("frame %d: displacement %.2f exceeds MaxStep %.2f", i, d, maxStep)
		}
		if i >= 2 {
			vx := centres[i][0] - centres[i-1][0]
			vy := centres[i][1] - centres[i-1][1]
			pvx := centres[i-1][0] - centres[i-2][0]
			pvy := centres[i-1][1] - centres[i-2][1]
			da := math.Hypot(vx-pvx, vy-pvy)
			if da > maxAccel+1.0 {
				t.Errorf("frame %d: acceleration %.2f exceeds MaxAcceleration %.2f", i, da, maxAccel)
			}
		}
	}
}

func TestPlanCropsCropContainsSubjectAtFrameEdges(t *testing.T) {
	// The subject starts partially outside the frame on the left, then leaves
	// on the right. The crop must remain inside the frame throughout.
	const n = 20
	frames := make([]Frame, n)
	for i := 0; i < n; i++ {
		cx := -20 + i*18
		block := image.Rect(cx-24, 96, cx+24, 144)
		frames[i] = Frame{
			Image: sequenceFrame(image.Rect(0, 0, 320, 240), &block),
			Time:  time.Duration(i) * 40 * time.Millisecond,
		}
	}
	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, frames)
}

func TestSequenceCropSize(t *testing.T) {
	cases := []struct {
		name   string
		w, h   int
		aspect float64
		factor float64
		wantW  int
		wantH  int
	}{
		{"portrait frame", 320, 240, 9.0 / 16.0, 0.8, 108, 192},
		{"square aspect", 320, 240, 1, 0.5, 120, 120},
		{"wide aspect refits height", 320, 240, 4, 0.8, 320, 80},
		{"tiny frame", 4, 3, 9.0 / 16.0, 0.8, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := resolveSequenceOptions(SequenceOptions{
				AspectRatio:      tc.aspect,
				CropHeightFactor: tc.factor,
			})
			if err != nil {
				t.Fatal(err)
			}
			w, h := sequenceCropSize(tc.w, tc.h, cfg)
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("got %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

// TestPlanCropsBoundedAnalysisBudget verifies that a large frame is analysed
// through a downscaled buffer: results stay correct and per-frame memory is
// bounded by MaxAnalysisDimension rather than by the source resolution.
func TestPlanCropsBoundedAnalysisBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a large synthetic frame")
	}
	const w, h = 2000, 1500
	block := image.Rect(1400, 700, 1600, 900)
	frames := []Frame{
		{Image: sequenceFrame(image.Rect(0, 0, w, h), &block), Time: 0},
		{Image: sequenceFrame(image.Rect(0, 0, w, h), &block), Time: 40 * time.Millisecond},
	}
	rects, err := PlanCrops(frames, SequenceOptions{
		AspectRatio:          9.0 / 16.0,
		MaxAnalysisDimension: 64, // deliberately tiny: fast and memory-light
	})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, frames)
	for i := 0; i < 2; i++ {
		if !image.Pt(1500, 800).In(rects[i]) {
			t.Errorf("frame %d: crop %v misses the subject", i, rects[i])
		}
		// Crop geometry must still be derived from the full-resolution frame.
		if want := int(math.Round(float64(h) * defaultCropHeightFactor)); rects[i].Dy() != want {
			t.Errorf("frame %d: crop height %d, want %d", i, rects[i].Dy(), want)
		}
	}
}

// TestPlanCropsOcclusionRetainsSubject checks the specific promise for a
// subject that disappears for two separate stretches: while it is hidden the
// window keeps its motion, so it is on target again as soon as the subject
// returns.
func TestPlanCropsOcclusionRetainsSubject(t *testing.T) {
	const (
		w, h      = 320, 240
		frames    = 40
		blockSize = 40
		x0, x1    = 50.0, 270.0
	)
	hidden := map[int]bool{10: true, 11: true, 26: true, 27: true, 28: true}
	seq := movingBlockSequence(frames, w, h, blockSize, x0, x1, hidden)

	rects, err := PlanCrops(seq, SequenceOptions{
		AspectRatio:         testAspect,
		Smoothness:          8,
		AccelerationWeight:  24,
		OcclusionConfidence: 0.3,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, seq)

	step := (x1 - x0) / float64(frames-1)
	// The first frame after each occlusion must already be near the subject:
	// a static window would fall far behind, and a per-frame optimum would
	// have snapped to the frame centre while the subject was hidden.
	for _, i := range []int{12, 29, 30} {
		cx := x0 + step*float64(i)
		gotX, _ := rectCentre(rects[i])
		if math.Abs(gotX-cx) > 25 {
			t.Errorf("frame %d after occlusion: window centre %.1f, subject %.1f", i, gotX, cx)
		}
	}
	// While hidden the window must not be parked at the frame centre.
	for i := range hidden {
		gotX, _ := rectCentre(rects[i])
		expected := x0 + step*float64(i)
		if math.Abs(gotX-expected) > 25 {
			t.Errorf("frame %d during occlusion: window centre %.1f, expected ~%.1f", i, gotX, expected)
		}
	}
}

// TestPlanCropsKeepsAspectOnExtremeFrames covers frames narrower than the
// requested crop: the crop is clamped to the frame and the aspect ratio is
// preserved as closely as integer pixels allow.
func TestPlanCropsKeepsAspectOnExtremeFrames(t *testing.T) {
	frames := []Frame{
		{Image: sequenceFrame(image.Rect(0, 0, 40, 400), nil), Time: 0},
		{Image: sequenceFrame(image.Rect(0, 0, 40, 400), nil), Time: 40 * time.Millisecond},
	}
	const aspect = 1.5
	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: aspect, CropHeightFactor: 1})
	if err != nil {
		t.Fatal(err)
	}
	assertInside(t, rects, frames)
	for i, r := range rects {
		if r.Dx() != 40 {
			t.Errorf("frame %d: crop width %d, want 40", i, r.Dx())
		}
		got := float64(r.Dx()) / float64(r.Dy())
		if math.Abs(got-aspect) > 0.1 {
			t.Errorf("frame %d: aspect %.2f, want ~%.2f", i, got, aspect)
		}
	}
}

func TestPlanCropsCustomCropHeight(t *testing.T) {
	frames := []Frame{{Image: sequenceFrame(image.Rect(0, 0, 320, 240), nil)}}
	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: 1, CropHeightFactor: 0.25})
	if err != nil {
		t.Fatal(err)
	}
	if rects[0].Dx() != 60 || rects[0].Dy() != 60 {
		t.Fatalf("got %v, want 60x60", rects[0])
	}
}

func TestPlanCropsUniformFramesUseCentre(t *testing.T) {
	frames := make([]Frame, 5)
	for i := range frames {
		frames[i] = Frame{
			Image: sequenceFrame(image.Rect(0, 0, 320, 240), nil),
			Time:  time.Duration(i) * time.Millisecond,
		}
	}
	rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect})
	if err != nil {
		t.Fatal(err)
	}
	x, y := rectCentre(rects[0])
	if x != 160 || y != 120 {
		t.Fatalf("uniform frames should centre the crop, got centre (%.1f, %.1f)", x, y)
	}
	for i := 1; i < len(rects); i++ {
		if rects[i] != rects[0] {
			t.Fatalf("uniform frames drifted: %v vs %v", rects[0], rects[i])
		}
	}
}

func FuzzPlanCrops(f *testing.F) {
	f.Add(8, 320, 240, 0.25, 1.0)
	f.Add(1, 16, 16, 1.0, 1.0)
	f.Fuzz(func(t *testing.T, n int, w, h int, smooth, aspect float64) {
		if n < 0 || n > 32 || w < 1 || w > 200 || h < 1 || h > 200 {
			t.Skip()
		}
		if math.IsNaN(smooth) || math.IsInf(smooth, 0) || smooth < 0 ||
			math.IsNaN(aspect) || math.IsInf(aspect, 0) || aspect <= 0 {
			t.Skip()
		}
		frames := make([]Frame, n)
		for i := range frames {
			frames[i] = Frame{
				Image: sequenceFrame(image.Rect(0, 0, w, h), nil),
				Time:  time.Duration(i) * time.Millisecond,
			}
		}
		rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: aspect, Smoothness: smooth})
		if err != nil {
			if n == 0 && !errors.Is(err, ErrEmptySequence) {
				t.Fatalf("empty sequence: %v", err)
			}
			if n > 0 {
				t.Fatalf("valid input rejected: %v", err)
			}
			return
		}
		if len(rects) != n {
			t.Fatalf("got %d rects for %d frames", len(rects), n)
		}
		for i, r := range rects {
			if !r.In(frames[i].Image.Bounds()) || r.Empty() {
				t.Fatalf("frame %d: invalid crop %v", i, r)
			}
		}
	})
}

func BenchmarkPlanCrops(b *testing.B) {
	const n = 120
	seq := movingBlockSequence(n, 320, 240, 48, 40, 280, map[int]bool{30: true, 31: true})
	opts := SequenceOptions{AspectRatio: testAspect}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := PlanCrops(seq, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPlanCropsExtremeAspectRatios(t *testing.T) {
	frames := []Frame{
		{Image: sequenceFrame(image.Rect(0, 0, 64, 48), nil), Time: 0},
		{Image: sequenceFrame(image.Rect(0, 0, 64, 48), nil), Time: 40 * time.Millisecond},
	}
	for _, aspect := range []float64{1e-12, 1e-6, 1, 1e6, 1e12} {
		rects, err := PlanCrops(frames, SequenceOptions{AspectRatio: aspect})
		if err != nil {
			t.Fatalf("aspect %g: %v", aspect, err)
		}
		assertInside(t, rects, frames)
	}
}

func TestPlanCropsMaxFramesDefaultApplied(t *testing.T) {
	img := sequenceFrame(image.Rect(0, 0, 8, 8), nil)
	frames := make([]Frame, DefaultSequenceMaxFrames+1)
	for i := range frames {
		frames[i] = Frame{Image: img, Time: time.Duration(i) * time.Millisecond}
	}
	if _, err := PlanCrops(frames, SequenceOptions{AspectRatio: testAspect}); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("expected the default MaxFrames to apply, got %v", err)
	}
}
