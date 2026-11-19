smartcrop
=========

[![Latest Release](https://img.shields.io/github/release/muesli/smartcrop.svg)](https://github.com/muesli/smartcrop/releases)
[![Build Status](https://github.com/muesli/smartcrop/workflows/build/badge.svg)](https://github.com/muesli/smartcrop/actions)
[![Coverage Status](https://coveralls.io/repos/github/muesli/smartcrop/badge.svg?branch=master)](https://coveralls.io/github/muesli/smartcrop?branch=master)
[![Go ReportCard](https://goreportcard.com/badge/muesli/smartcrop)](https://goreportcard.com/report/muesli/smartcrop)
[![GoDoc](https://godoc.org/github.com/golang/gddo?status.svg)](https://godoc.org/github.com/muesli/smartcrop)

smartcrop finds good image crops for arbitrary sizes. It is a pure Go implementation, based on Jonas Wagner's [smartcrop.js](https://github.com/jwagner/smartcrop.js)

![Example](./examples/gopher.jpg)
Image: [https://www.flickr.com/photos/usfwspacific/8182486789](https://www.flickr.com/photos/usfwspacific/8182486789) by Washington Dept of Fish and Wildlife, originally licensed under [CC-BY-2.0](https://creativecommons.org/licenses/by/2.0/) when the image was imported back in September 2014

![Example](./examples/goodtimes.jpg)
Image: [https://www.flickr.com/photos/endogamia/5682480447](https://www.flickr.com/photos/endogamia/5682480447) by Leon F. Cabeiro (N. Feans), licensed under [CC-BY-2.0](https://creativecommons.org/licenses/by/2.0/)

## Installation

Make sure you have a working Go environment (Go 1.12 or higher is required).
See the [install instructions](https://golang.org/doc/install.html).

To install smartcrop, simply run:

    go get github.com/muesli/smartcrop

To compile it from source:

    git clone https://github.com/muesli/smartcrop.git
    cd smartcrop
    go build

## Example
```go
package main

import (
	"fmt"
	"image"
	_ "image/png"
	"os"

	"github.com/muesli/smartcrop"
	"github.com/muesli/smartcrop/nfnt"
)

func main() {
	f, _ := os.Open("image.png")
	img, _, _ := image.Decode(f)

	analyzer := smartcrop.NewAnalyzer(nfnt.NewDefaultResizer())
	topCrop, _ := analyzer.FindBestCrop(img, 250, 250)

	// The crop will have the requested aspect ratio, but you need to copy/scale it yourself
	fmt.Printf("Top crop: %+v\n", topCrop)

	type SubImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	croppedimg := img.(SubImager).SubImage(topCrop)
	// ...
}
```

Also see the test cases in smartcrop_test.go and cli application in cmd/smartcrop/ for further working examples.

## Layout-safe crops (title & price overlays)

`FindBestCropWithLayout` plans a crop together with the placement of one or
more text boxes (a title, a price, ...). It reuses the regular significance
score, then jointly penalises detail and skin coverage underneath the boxes so
faces and busy subjects are not pushed into the copy area:

```go
analyzer := smartcrop.NewLayoutAnalyzer(nfnt.NewDefaultResizer())

layout, err := analyzer.FindBestCropWithLayout(img, smartcrop.LayoutOptions{
    Width:  1200, // target aspect ratio and output coordinate space
    Height: 630,
    Boxes: []smartcrop.TextBox{
        {Width: 520, Height: 96},  // title
        {Width: 260, Height: 64},  // price
    },
    Anchors: []smartcrop.Anchor{       // optional, defaults to the 4 corners
        smartcrop.AnchorBottomRight,
        smartcrop.AnchorBottomLeft,
    },
    Margin: 40, // distance from the crop edge
    Gap:    24, // distance between boxes
})
if err != nil {
    // ErrInvalidDimensions, ErrUnsupportedAspectRatio, ErrTextDoesNotFit, ...
}

fmt.Println(layout.Crop)          // rectangle in source image coordinates
for _, box := range layout.Boxes { // output coordinates, relative to Crop
    fmt.Println(box.Anchor, box.Rect)
}
fmt.Printf("score %.4f (plain best %.4f), text penalty %.4f, fallback %t\n",
    layout.LayoutScore, layout.BestBaseScore, layout.TextPenalty, layout.Fallback)
```

Guarantees and failure modes:

* Boxes never overlap each other, never leave the crop, and respect `Margin`
  and `Gap`; placement happens in the output coordinate space.
* The subject is preserved: crops retaining less than
  `MinScoreRetention` (default 50%) of the best plain crop's score are not
  considered, so the planner never runs off to an empty background.
* If the image has no sufficiently quiet region, the quietest valid placement
  is returned with `Fallback == true` instead of failing.
* Invalid input (zero/negative sizes, unknown anchors, negative margins,
  aspect ratios beyond 20:1) and impossible geometry return exported errors:
  `ErrNoTextBoxes`, `ErrInvalidTextBox`, `ErrInvalidAnchor`,
  `ErrInvalidLayoutOptions`, `ErrUnsupportedAspectRatio`,
  `ErrCropLargerThanImage`, `ErrTextDoesNotFit`, `ErrNoValidPlacement` and
  `ErrTooManyCandidates`.
* Transparent pixels are composited over white. Images whose `Bounds` do not
  start at the origin are handled correctly, and the returned `Crop` is in the
  source image's own coordinate space.
* The candidate budget (`MaxCandidates`, default 2,000,000 crop × placement
  combinations) bounds work on very large candidate sets.

## Simple CLI application

    go install github.com/muesli/smartcrop/cmd/smartcrop

    Usage of smartcrop:
      -height int
            crop height
      -input string
            input filename
      -output string
            output filename
      -quality int
            jpeg quality (default 85)
      -resize
            resize after cropping (default true)
      -width int
            crop width

Example:
    smartcrop -input examples/gopher.jpg -output gopher_cropped.jpg -width 300 -height 150

## Sample Data

You can find a bunch of test images for the algorithm [here](https://github.com/muesli/smartcrop-samples).

## Feedback

Got some feedback or suggestions? Please open an issue or drop me a note!

* [Twitter](https://twitter.com/mueslix)
* [The Fediverse](https://mastodon.social/@fribbledom)
