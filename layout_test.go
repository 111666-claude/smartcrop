package smartcrop

import (
	"image"
	"image/color"
	_ "image/jpeg"
	"math"
	"os"
	"testing"

	"github.com/muesli/smartcrop/nfnt"
)

// personCorners are the four placements used by the layout regression tests.
var personCorners = []struct {
	name string
	rect image.Rectangle
}{
	{"top-left", image.Rect(10, 10, 130, 130)},
	{"top-right", image.Rect(270, 10, 390, 130)},
	{"bottom-left", image.Rect(10, 270, 130, 390)},
	{"bottom-right", image.Rect(270, 270, 390, 390)},
}

// layoutFixture draws a 400x400 scene with a skin-toned figure in the given
// corner and a smooth, detail-free background everywhere else. The figure is
// a hard-edged block, so a crop-boundary slice of it is possible but never a
// good outcome for a valid placement.
func layoutFixture(person image.Rectangle) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			shade := uint8(180 + x/10)
			img.SetRGBA(x, y, color.RGBA{shade, shade, shade, 255})
			if image.Pt(x, y).In(person) {
				img.SetRGBA(x, y, color.RGBA{199, 145, 112, 255})
			}
		}
	}
	return img
}

// busyFixture is a blank scene with a high-detail checkerboard block in the
// bottom-right corner.
func busyFixture() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			shade := uint8(180 + x/10)
			img.SetRGBA(x, y, color.RGBA{shade, shade, shade, 255})
		}
	}
	busy := image.Rect(240, 240, 395, 395)
	for y := busy.Min.Y; y < busy.Max.Y; y++ {
		for x := busy.Min.X; x < busy.Max.X; x++ {
			h := uint32(x*374761393 + y*668265263)
			h = (h ^ (h >> 13)) * 1274126177
			shade := uint8((h >> 24) & 0xff)
			img.SetRGBA(x, y, color.RGBA{shade, shade, shade, 255})
		}
	}
	return img
}

func layoutAnalyzer() LayoutAnalyzer {
	return NewLayoutAnalyzer(nfnt.NewDefaultResizer())
}

var cornerAnchors = []Anchor{AnchorTopLeft, AnchorTopRight, AnchorBottomLeft, AnchorBottomRight}

func testLayoutOptions(boxes ...TextBox) LayoutOptions {
	return LayoutOptions{
		Width:   300,
		Height:  300,
		Boxes:   boxes,
		Anchors: cornerAnchors,
		Margin:  16,
		Gap:     8,
	}
}

func checkPlacedBoxes(t *testing.T, layout CropLayout, opts LayoutOptions) {
	t.Helper()

	if len(layout.Boxes) != len(opts.Boxes) {
		t.Fatalf("expected %d boxes, got %d", len(opts.Boxes), len(layout.Boxes))
	}

	candidates := opts.Anchors
	if len(candidates) == 0 {
		candidates = []Anchor{AnchorTopLeft, AnchorTopRight, AnchorBottomLeft, AnchorBottomRight}
	}

	for i, placed := range layout.Boxes {
		if placed.Width != opts.Boxes[i].Width || placed.Height != opts.Boxes[i].Height {
			t.Errorf("box %d: expected size %dx%d, got %dx%d", i,
				opts.Boxes[i].Width, opts.Boxes[i].Height, placed.Width, placed.Height)
		}
		if !placed.Rect.In(image.Rect(0, 0, opts.Width, opts.Height)) {
			t.Errorf("box %d: %v is outside the %dx%d output", i, placed.Rect, opts.Width, opts.Height)
		}
		if placed.Rect.Min.X < opts.Margin || placed.Rect.Min.Y < opts.Margin ||
			opts.Width-placed.Rect.Max.X < opts.Margin || opts.Height-placed.Rect.Max.Y < opts.Margin {
			t.Errorf("box %d: %v violates margin %d", i, placed.Rect, opts.Margin)
		}
		found := false
		for _, anchor := range candidates {
			if placed.Anchor == anchor {
				found = true
			}
		}
		if !found {
			t.Errorf("box %d: anchor %v was not a candidate", i, placed.Anchor)
		}
		if want := anchorRectangle(placed.Anchor, placed.TextBox,
			image.Rect(opts.Margin, opts.Margin, opts.Width-opts.Margin, opts.Height-opts.Margin)); placed.Rect != want {
			t.Errorf("box %d: rect %v does not match anchor %v (%v)", i, placed.Rect, placed.Anchor, want)
		}
	}

	for i := 0; i < len(layout.Boxes); i++ {
		for j := i + 1; j < len(layout.Boxes); j++ {
			a, b := layout.Boxes[i].Rect, layout.Boxes[j].Rect
			gap := opts.Gap
			if a.Min.X-gap < b.Max.X && b.Min.X-gap < a.Max.X &&
				a.Min.Y-gap < b.Max.Y && b.Min.Y-gap < a.Max.Y {
				t.Errorf("boxes %d and %d overlap or violate gap %d: %v and %v", i, j, gap, a, b)
			}
		}
	}
}

// TestLayoutAvoidsPersonInEachCorner places a person in each corner of a
// fixed scene and checks that the title and price boxes never land on the
// figure, end up quiet, and improve on a forced same-corner layout.
func TestLayoutAvoidsPersonInEachCorner(t *testing.T) {
	boxes := []TextBox{
		{Width: 190, Height: 34}, // title
		{Width: 110, Height: 24}, // price
	}
	opts := testLayoutOptions(boxes...)

	for _, corner := range personCorners {
		corner := corner
		t.Run(corner.name, func(t *testing.T) {
			img := layoutFixture(corner.rect)
			layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
			if err != nil {
				t.Fatalf("FindBestCropWithLayout: %v", err)
			}

			checkPlacedBoxes(t, layout, opts)

			if layout.Crop.Empty() || !layout.Crop.In(img.Bounds()) {
				t.Fatalf("crop %v is not inside %v", layout.Crop, img.Bounds())
			}
			if ratio := float64(layout.Crop.Dx()) / float64(layout.Crop.Dy()); math.Abs(ratio-1) > 0.01 {
				t.Errorf("expected square crop, got %v (ratio %.3f)", layout.Crop, ratio)
			}

			// The main subject must stay in frame: the corner figure is the
			// only significant content in the fixture.
			if !corner.rect.In(layout.Crop) {
				t.Errorf("crop %v does not retain the subject at %v", layout.Crop, corner.rect)
			}

			// Neither box may cover the figure once the crop is mapped back to
			// source coordinates.
			for i, placed := range layout.Boxes {
				rect := mapSourceBox(layout.Crop, placed.Rect, opts.Width, opts.Height)
				if rect.Overlaps(corner.rect) {
					t.Errorf("box %d %v covers the person at %v (overlap %v)",
						i, placed.Rect, corner.rect, rect.Intersect(corner.rect))
				}
				if skin := sourceSkinCoverage(img, rect); skin > 0 {
					t.Errorf("box %d has %.3f skin coverage underneath", i, skin)
				}
			}

			// The chosen placement must be at least as quiet as the layout in
			// which both boxes are stacked in the person's corner.
			figureAnchor := anchorNearestCorner(corner.rect, opts)
			forced := []image.Rectangle{
				anchorRectangle(figureAnchor, boxes[0], outputInner(opts)),
				anchorRectangle(figureAnchor, boxes[1], outputInner(opts)),
			}
			plain, err := smartCrop(img, opts.Width, opts.Height)
			if err != nil {
				t.Fatalf("plain crop: %v", err)
			}
			forcedPenalty := newLayoutScorer(detectionImage(lowImageFor(t, img))).
				placementPenalty(plain, forced, opts.Width, opts.Height)
			if layout.TextPenalty >= forcedPenalty {
				t.Errorf("layout penalty %.4f did not improve on the forced corner %.4f",
					layout.TextPenalty, forcedPenalty)
			}
			if layout.Fallback {
				t.Errorf("expected enough empty space, got fallback (penalty %.4f)", layout.TextPenalty)
			}

			t.Logf("corner=%s crop=%v boxes=%v penalty=%.4f forced=%.4f base=%.4f layout=%.4f",
				corner.name, layout.Crop, layout.Boxes, layout.TextPenalty, forcedPenalty,
				layout.BaseScore, layout.LayoutScore)
		})
	}
}

// TestLayoutPrefersQuietCorner verifies the joint score moves a box away from
// a busy texture block even though the block carries no skin.
func TestLayoutPrefersQuietCorner(t *testing.T) {
	img := busyFixture()
	opts := testLayoutOptions(TextBox{Width: 210, Height: 120})

	layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout: %v", err)
	}
	checkPlacedBoxes(t, layout, opts)

	if layout.Boxes[0].Anchor == AnchorBottomRight {
		t.Errorf("box was placed on the busy bottom-right corner: %v", layout.Boxes[0])
	}

	plain, err := smartCrop(img, opts.Width, opts.Height)
	if err != nil {
		t.Fatalf("plain crop: %v", err)
	}
	busy := []image.Rectangle{anchorRectangle(AnchorBottomRight, opts.Boxes[0], outputInner(opts))}
	busyPenalty := newLayoutScorer(detectionImage(lowImageFor(t, img))).
		placementPenalty(plain, busy, opts.Width, opts.Height)
	if layout.TextPenalty > 0.05 {
		t.Errorf("chosen placement is not quiet: penalty %.4f", layout.TextPenalty)
	}
	if layout.TextPenalty >= busyPenalty {
		t.Errorf("chosen penalty %.4f is not lower than busy-corner penalty %.4f",
			layout.TextPenalty, busyPenalty)
	}
	if busyPenalty < 0.05 {
		t.Fatalf("fixture is not busy enough to be meaningful: %.4f", busyPenalty)
	}
}

// TestLayoutFallbackWhenNoQuietSpace checks the deterministic fallback when
// the only allowed anchor sits on the subject.
func TestLayoutFallbackWhenNoQuietSpace(t *testing.T) {
	img := layoutFixture(personCorners[0].rect)
	opts := testLayoutOptions(TextBox{Width: 200, Height: 60})
	opts.Anchors = []Anchor{AnchorTopLeft}

	layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout: %v", err)
	}
	checkPlacedBoxes(t, layout, opts)
	if !layout.Fallback {
		t.Errorf("expected Fallback with a subject under the only anchor, penalty %.4f", layout.TextPenalty)
	}
	if layout.TextPenalty <= DefaultMaxTextPenalty {
		t.Errorf("expected penalty above %.2f, got %.4f", DefaultMaxTextPenalty, layout.TextPenalty)
	}
}

// TestLayoutScoresAreConsistent checks the reported score bookkeeping and that
// the layout crop keeps the main subject's significance.
func TestLayoutScoresAreConsistent(t *testing.T) {
	img := layoutFixture(personCorners[3].rect)
	opts := testLayoutOptions(TextBox{Width: 190, Height: 34}, TextBox{Width: 110, Height: 24})

	layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout: %v", err)
	}

	wantLayoutScore := layout.BaseScore/layout.BestBaseScore -
		layoutPenaltyWeight*layout.TextPenalty
	if math.Abs(layout.LayoutScore-wantLayoutScore) > 1e-9 {
		t.Errorf("LayoutScore = %.6f, want %.6f", layout.LayoutScore, wantLayoutScore)
	}
	if layout.LayoutScore > 1 {
		t.Errorf("LayoutScore %.6f exceeds 1", layout.LayoutScore)
	}
	if layout.BestBaseScore <= 0 || layout.BaseScore > layout.BestBaseScore {
		t.Errorf("base score %.6f is not <= best plain score %.6f", layout.BaseScore, layout.BestBaseScore)
	}
	if layout.TextPenalty < 0 {
		t.Errorf("negative TextPenalty: %f", layout.TextPenalty)
	}
	if layout.Fallback != (layout.TextPenalty > DefaultMaxTextPenalty) {
		t.Errorf("Fallback = %t, want %t", layout.Fallback, layout.TextPenalty > DefaultMaxTextPenalty)
	}
	if layout.BaseScore <= 0 {
		t.Errorf("expected a positive base score, got %f", layout.BaseScore)
	}

	// Re-measuring the chosen placement against the same detection image must
	// reproduce TextPenalty.
	detects := detectionImage(lowImageFor(t, img))
	penalty := newLayoutScorer(detects).placementPenalty(layout.Crop, boxRects(layout.Boxes), opts.Width, opts.Height)
	if math.Abs(penalty-layout.TextPenalty) > 1e-9 {
		t.Errorf("re-measured penalty %.6f, want %.6f", penalty, layout.TextPenalty)
	}

	// The layout crop must retain most of the best plain crop's significance
	// so the subject is not pushed out of frame.
	plain, err := smartCrop(img, opts.Width, opts.Height)
	if err != nil {
		t.Fatalf("plain crop: %v", err)
	}
	plainCrop := Crop{Rectangle: plain}
	plainCrop.Score = score(detects, plainCrop)
	plainScore := plainCrop.totalScore()
	if plainScore <= 0 {
		t.Fatalf("fixture produced a non-positive plain score: %f", plainScore)
	}
	if layout.BaseScore < plainScore*DefaultMinScoreRetention {
		t.Errorf("BaseScore %.6f dropped below %.0f%% of plain %.6f",
			layout.BaseScore, DefaultMinScoreRetention*100, plainScore)
	}
}

// TestLayoutExtremeAnchorsDoNotOverlap forces both boxes onto the same anchor
// and expects the planner to reject the placement.
func TestLayoutExtremeAnchorsDoNotOverlap(t *testing.T) {
	img := layoutFixture(personCorners[0].rect)
	opts := testLayoutOptions(TextBox{Width: 190, Height: 34}, TextBox{Width: 110, Height: 24})
	opts.Anchors = []Anchor{AnchorBottomRight}

	if _, err := layoutAnalyzer().FindBestCropWithLayout(img, opts); err != ErrNoValidPlacement {
		t.Fatalf("expected ErrNoValidPlacement, got %v", err)
	}
}

// TestLayoutErrors covers deterministic error returns for impossible input.
func TestLayoutErrors(t *testing.T) {
	img := layoutFixture(personCorners[0].rect)

	base := func() LayoutOptions {
		return testLayoutOptions(TextBox{Width: 100, Height: 20})
	}

	tests := []struct {
		name string
		opts LayoutOptions
		img  image.Image
		want error
	}{
		{
			name: "zero width",
			opts: func() LayoutOptions {
				o := base()
				o.Width = 0
				return o
			}(),
			want: ErrInvalidDimensions,
		},
		{
			name: "extreme aspect",
			opts: func() LayoutOptions {
				o := base()
				o.Width = 400
				o.Height = 10
				return o
			}(),
			want: ErrUnsupportedAspectRatio,
		},
		{
			name: "no boxes",
			opts: func() LayoutOptions {
				o := base()
				o.Boxes = nil
				return o
			}(),
			want: ErrNoTextBoxes,
		},
		{
			name: "zero sized box",
			opts: func() LayoutOptions {
				o := base()
				o.Boxes = []TextBox{{Width: 0, Height: 20}}
				return o
			}(),
			want: ErrInvalidTextBox,
		},
		{
			name: "unknown anchor",
			opts: func() LayoutOptions {
				o := base()
				o.Anchors = []Anchor{anchorCount}
				return o
			}(),
			want: ErrInvalidAnchor,
		},
		{
			name: "negative margin",
			opts: func() LayoutOptions {
				o := base()
				o.Margin = -1
				return o
			}(),
			want: ErrInvalidLayoutOptions,
		},
		{
			name: "box does not fit",
			opts: func() LayoutOptions {
				o := base()
				o.Boxes = []TextBox{{Width: 400, Height: 20}}
				return o
			}(),
			want: ErrTextDoesNotFit,
		},
		{
			name: "degenerate image",
			opts: base(),
			img:  image.NewRGBA(image.Rect(0, 0, 1, 1)),
			want: ErrCropLargerThanImage,
		},
		{
			name: "candidate budget",
			opts: func() LayoutOptions {
				o := base()
				o.MaxCandidates = 1
				return o
			}(),
			want: ErrTooManyCandidates,
		},
		{
			name: "too many boxes",
			opts: func() LayoutOptions {
				o := base()
				o.Boxes = make([]TextBox, maxLayoutTextBoxes+1)
				for i := range o.Boxes {
					o.Boxes[i] = TextBox{Width: 10, Height: 10}
				}
				return o
			}(),
			want: ErrTooManyCandidates,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			src := test.img
			if src == nil {
				src = img
			}
			if _, err := layoutAnalyzer().FindBestCropWithLayout(src, test.opts); err != test.want {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

// TestLayoutTransparentBackground verifies that alpha is composited over
// white: a fully transparent image is treated as blank.
func TestLayoutTransparentBackground(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 400))
	opts := testLayoutOptions(TextBox{Width: 190, Height: 34}, TextBox{Width: 110, Height: 24})

	layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout: %v", err)
	}
	checkPlacedBoxes(t, layout, opts)
	if layout.Fallback {
		t.Errorf("blank image should have quiet space, got fallback (penalty %.4f)", layout.TextPenalty)
	}
	if layout.TextPenalty != 0 {
		t.Errorf("expected zero penalty on a blank image, got %.6f", layout.TextPenalty)
	}
}

// TestLayoutNonZeroBounds ensures crops are reported in the source image's own
// coordinate space when Bounds does not start at the origin.
func TestLayoutNonZeroBounds(t *testing.T) {
	offset := image.Pt(50, 70)
	source := layoutFixture(personCorners[2].rect)
	img := image.NewRGBA(image.Rect(offset.X, offset.Y, offset.X+400, offset.Y+400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			img.SetRGBA(offset.X+x, offset.Y+y, source.RGBAAt(x, y))
		}
	}

	opts := testLayoutOptions(TextBox{Width: 190, Height: 34}, TextBox{Width: 110, Height: 24})
	layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout: %v", err)
	}
	checkPlacedBoxes(t, layout, opts)

	if !layout.Crop.In(img.Bounds()) {
		t.Fatalf("crop %v is not inside source bounds %v", layout.Crop, img.Bounds())
	}
	if layout.Crop.Min.X < offset.X || layout.Crop.Min.Y < offset.Y {
		t.Errorf("crop %v was not translated by the source offset %v", layout.Crop, offset)
	}

	zeroOrigin, err := layoutAnalyzer().FindBestCropWithLayout(source, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout (zero origin): %v", err)
	}
	if zeroOrigin.Crop.Add(offset) != layout.Crop {
		t.Errorf("crop %v, want %v after adding offset", layout.Crop, zeroOrigin.Crop.Add(offset))
	}
	if zeroOrigin.TextPenalty != layout.TextPenalty {
		t.Errorf("penalty changed with bounds offset: %.6f vs %.6f", layout.TextPenalty, zeroOrigin.TextPenalty)
	}
}

// TestLayoutDeterministic checks that repeated runs on the same input return
// identical results.
func TestLayoutDeterministic(t *testing.T) {
	img := layoutFixture(personCorners[1].rect)
	opts := testLayoutOptions(TextBox{Width: 190, Height: 34}, TextBox{Width: 110, Height: 24})

	first, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
	if err != nil {
		t.Fatalf("FindBestCropWithLayout: %v", err)
	}
	for i := 0; i < 3; i++ {
		next, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if next.Crop != first.Crop || next.TextPenalty != first.TextPenalty {
			t.Fatalf("run %d returned %v/%.6f, want %v/%.6f", i,
				next.Crop, next.TextPenalty, first.Crop, first.TextPenalty)
		}
		for j := range next.Boxes {
			if next.Boxes[j].Rect != first.Boxes[j].Rect || next.Boxes[j].Anchor != first.Boxes[j].Anchor {
				t.Fatalf("run %d box %d = %v/%v, want %v/%v", i, j,
					next.Boxes[j].Rect, next.Boxes[j].Anchor, first.Boxes[j].Rect, first.Boxes[j].Anchor)
			}
		}
	}
}

// mapSourceBox maps an output-space text box back into source coordinates for
// the given crop.
func mapSourceBox(crop image.Rectangle, box image.Rectangle, outWidth, outHeight int) image.Rectangle {
	scaleX := float64(crop.Dx()) / float64(outWidth)
	scaleY := float64(crop.Dy()) / float64(outHeight)
	return image.Rect(
		crop.Min.X+int(math.Floor(float64(box.Min.X)*scaleX)),
		crop.Min.Y+int(math.Floor(float64(box.Min.Y)*scaleY)),
		crop.Min.X+int(math.Ceil(float64(box.Max.X)*scaleX)),
		crop.Min.Y+int(math.Ceil(float64(box.Max.Y)*scaleY)),
	)
}

func sourceSkinCoverage(img *image.RGBA, rect image.Rectangle) float64 {
	rect = rect.Intersect(img.Bounds())
	if rect.Empty() {
		return 0
	}
	var skin, samples float64
	for y := rect.Min.Y; y < rect.Max.Y; y += 2 {
		for x := rect.Min.X; x < rect.Max.X; x += 2 {
			c := img.RGBAAt(x, y)
			if c.R > 180 && c.G > 120 && c.G < 170 && c.B > 90 && c.B < 140 {
				skin++
			}
			samples++
		}
	}
	return skin / samples
}

func anchorNearestCorner(rect image.Rectangle, opts LayoutOptions) Anchor {
	center := rect.Min.Add(rect.Max).Div(2)
	horizontal := AnchorTopLeft
	if center.X > opts.Width/2 {
		horizontal = AnchorTopRight
	}
	if center.Y > opts.Height/2 {
		if horizontal == AnchorTopRight {
			return AnchorBottomRight
		}
		return AnchorBottomLeft
	}
	return horizontal
}

func outputInner(opts LayoutOptions) image.Rectangle {
	return image.Rect(opts.Margin, opts.Margin, opts.Width-opts.Margin, opts.Height-opts.Margin)
}

func boxRects(boxes []PlacedTextBox) []image.Rectangle {
	rects := make([]image.Rectangle, len(boxes))
	for i := range boxes {
		rects[i] = boxes[i].Rect
	}
	return rects
}

func lowImageFor(t *testing.T, img image.Image) *image.RGBA {
	t.Helper()
	low, prescalefactor := prepareLowImage(nfnt.NewDefaultResizer(), normalizeImage(img))
	if prescalefactor != 1.0 {
		t.Fatalf("fixture is expected to skip prescaling, got factor %f", prescalefactor)
	}
	return low
}

// TestLayoutRealImages runs the planner over the bundled example photos and
// checks the result invariants hold on real content.
func TestLayoutRealImages(t *testing.T) {
	for _, file := range []string{"./examples/gopher.jpg", "./examples/goodtimes.jpg"} {
		file := file
		t.Run(file, func(t *testing.T) {
			fi, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			img, _, err := image.Decode(fi)
			if closeErr := fi.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}

			opts := LayoutOptions{
				Width:  250,
				Height: 250,
				Boxes:  []TextBox{{Width: 90, Height: 22}, {Width: 60, Height: 16}},
				Margin: 8,
				Gap:    4,
			}

			layout, err := layoutAnalyzer().FindBestCropWithLayout(img, opts)
			if err != nil {
				t.Fatalf("FindBestCropWithLayout: %v", err)
			}
			checkPlacedBoxes(t, layout, opts)

			if !layout.Crop.In(img.Bounds()) || layout.Crop.Empty() {
				t.Fatalf("crop %v is not inside %v", layout.Crop, img.Bounds())
			}
			if ratio := float64(layout.Crop.Dx()) / float64(layout.Crop.Dy()); math.Abs(ratio-1) > 0.02 {
				t.Errorf("expected near-square crop, got %v (ratio %.3f)", layout.Crop, ratio)
			}
			if layout.TextPenalty < 0 || layout.TextPenalty > 1 {
				t.Errorf("TextPenalty %.4f outside [0, 1]", layout.TextPenalty)
			}
			if layout.BaseScore < layout.BestBaseScore*DefaultMinScoreRetention {
				t.Errorf("BaseScore %.6f below retention floor of %.6f", layout.BaseScore, layout.BestBaseScore)
			}

			// The crop must still be a plausible plain crop: it may not be
			// completely outside the significance range of the plain result.
			plain, err := smartCrop(img, opts.Width, opts.Height)
			if err != nil {
				t.Fatalf("plain crop: %v", err)
			}
			plainCrop := Crop{Rectangle: plain}
			plainCrop.Score = score(detectionImage(lowImageFor(t, img)), plainCrop)
			if layout.BaseScore < plainCrop.totalScore()*DefaultMinScoreRetention {
				t.Errorf("layout crop %v lost the subject vs plain crop %v", layout.Crop, plain)
			}
		})
	}
}
