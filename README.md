# GoTiler Core

<p align="center">
  <img src="gotiler-core-banner.png" alt="Gotiler Repository Banner" width="100%">
</p>

`gotiler-core` is the **high quality**, **extra fast**, **out-of-core** point-cloud tiling engine used by the `gotiler` CLI and released as Open Source code. It reads and converts point clouds into OGC 3D Tiles 1.0 or 1.1.


## ✅ Supported features

- ⚡ **Extremely fast**, **out of core**, tiling algorithm that beats the alternatives. Tile XXL point clouds in minutes with minimal memory usage. 
- 🌟 **High quality output**, with approximately uniform and predictable tile sizes.
- 🌐 **Embedded PROJ-based coordinate reprojection** for state of the art coordinate.
- 🚀 Supports the newest **OGC 3D Tiles 1.1** format (.glb) in addition to the legacy 3D Tiles 1.0 (.pnts).
- 🎛️ **Customizable**: choose between Refine mode Add or Replace, define the expected tile size, choose the attributes you want to export and more. 
- 🔍 Extract **CRS metadata directly from LAS/LAZ VLRs**, or pass in a EPSG code, Proj4 or WKT string.

## ⚠️ Extra features

Additional features like:

- 3D Tiles 1.1 **point cloud compression and quantization**
- **e57** file support
- Automatic **point cloud subsampling**
- Direct **S3 uploads**
- And others...

are available to be licensed separately and not included in this repository. 

This model helps to keep the development of the tool sustainable.

If interested in these features, please get in touch at info@gotiler.io

## ❤️ Support the Project

`gotiler-core` is an open-source project maintained with love. If the library saves you time or resources, consider **[making a donation on Ko-fi](https://ko-fi.com/mfbonfigli)** to support ongoing maintenance.

If you are using this engine in a commercial environment or need features like **E57 support** or **meshopt compression**, please support the project sustainably by inquiring about our **Commercial/Dual Licensing options** at info@gotiler.io.

## 📜 License

This repository is released under GNU Affero General Public License v3. See [LICENSE.md](LICENSE.md).

## 💼 Dual Licensing

Please send an email to info@gotiler.io for inquiries regarding alternative licensing options if AGPLv3 is too restrictive for your case.

## 🍯 Public API

The public API is intentionally small and centered on the `tiler` package:

| Package | Purpose |
|---------|---------|
| `tiler` | Main tiling API, options, progress callbacks |
| `tiler/model` | Point, transform, attributes, refine mode |
| `tiler/mutator` | Built-in point mutators and mutator interface |
| `tiler/pointcloud` | Public point-cloud reader interface |
| `tiler/plugin` | Registries and hooks |
| `tiler/tree` | Public tree interfaces |
| `tiler/encoding` | Shared helpers for tile encoders: attribute column resolution, output naming, per-format type mappings |
| `version` | Tileset version constants and parsing |

## 🛠️ Usage

Import the module from Go code:

```go
import "github.com/mfbonfigli/gotiler-core/tiler"
```

## Basic Usage

### Tile One File

```go
package main

import (
	"context"
	"log"

	"github.com/mfbonfigli/gotiler-core/tiler"
)

func main() {
	t, err := tiler.NewGoTiler()
	if err != nil {
		log.Fatal(err)
	}

	opts := tiler.NewDefaultTilerOptions()
	err = t.ProcessFiles(
		[]string{`C:\data\scan.las`},
		`C:\data\tiles`,
		"", // empty means autodetect CRS when the input format supports it
		opts,
		context.Background(),
	)
	if err != nil {
		log.Fatal(err)
	}
}
```

### Tile Several Files As One Tileset

```go
opts := tiler.NewTilerOptions(
	tiler.WithPointsPerTile(75_000),
)

err := t.ProcessFiles(
	[]string{
		`C:\data\part-1.laz`,
		`C:\data\part-2.laz`,
	},
	`C:\data\merged-tiles`,
	"EPSG:32633",
	opts,
	context.Background(),
)
```

### Tile Every File In A Folder Separately

```go
err := t.ProcessFolder(
	`C:\data\point-clouds`,
	`C:\data\tiles`,
	"EPSG:32633",
	tiler.NewDefaultTilerOptions(),
	context.Background(),
)
```

`ProcessFolder` writes one tileset per input file, using the input file name as
the output subfolder name.

## Input And CRS Notes

Built-in input readers are `.las` and `.laz`. LAS and LAZ inputs can autodetect CRS from supported GeoTIFF or WKT metadata
when `sourceCRS` is empty.

All points are converted to EPSG:4978 internally and stored in a local Z-up
coordinate frame before writing 3D Tiles.

## Options

Create options with `tiler.NewDefaultTilerOptions()` or use
`tiler.NewTilerOptions(...)` with option functions.

| Option | Default | Description |
|--------|---------|-------------|
| `WithWorkerNumber(n)` | `runtime.NumCPU()` | Number of worker goroutines used by loading, tree building, and writing |
| `WithPointsPerTile(n)` | `50000` | Target maximum points per output tile |
| `WithEncoder(id)` | `plugin.EncoderGLB` | Selects the registered output encoder; the encoder controls tileset version and content filename |
| `WithEightBitColors(true)` | `false` | Treat LAS/LAZ RGB values as 8-bit instead of 16-bit |
| `WithMutators(m)` | none | Applies point transformations or filtering before tree insertion |
| `WithRefineMode(r)` | `model.RefineAdd` | Sets tileset refine mode: `ADD` or `REPLACE` |
| `WithGECorrection(c)` | `1.0` | Multiplies geometric error values written to `tileset.json` |
| `WithInitialGeometricError(ge)` | `0` | Sets a minimum root geometric error target; `0` uses the automatic value |
| `WithAttributes(attrs)` | intensity, classification | Selects optional per-point attributes to export |
| `WithProgressCallback(cb)` | none | Receives progress events while tiling |
| `WithWriterProvider(wp)` | disk writer | Allows injecting a custom writer |
| `WithWriterMiddleware(mw...)` | none | Wraps writer around other writers for custom behaviors |
| `WithWriterFinalizer(f...)` | none | Runs hooks after all content and `tileset.json` are written |
| `WithTreeProvider(provider)` | internal KD tree | Allows injecting the tree logic |

### Output Encoder

```go
import "github.com/mfbonfigli/gotiler-core/tiler/plugin"

opts := tiler.NewTilerOptions(
	tiler.WithEncoder(plugin.EncoderGLB),
)
```

Default encoders are:

| Encoder ID | Output | Tileset version |
|------------|--------|-----------------|
| `plugin.EncoderPNTS` / `"pnts"` | `.pnts` | `1.0` |
| `plugin.EncoderGLB` / `"glb"` | `.glb` | `1.1` |

To write legacy `.pnts` content:

```go
opts := tiler.NewTilerOptions(
	tiler.WithEncoder(plugin.EncoderPNTS),
)
```

### Attributes

Any per-point attribute exposed by the input files can be requested by name,
matched case-insensitively and ignoring whitespace:

```go
import "github.com/mfbonfigli/gotiler-core/tiler/model"

opts := tiler.NewTilerOptions(
	tiler.WithAttributes(model.NewAttributes(
		model.AttrIntensity,
		"gps_time",
		"my_custom_field", // e.g. a LAS extra-byte attribute
	)),
)
```

Constants exist for the standard names: `model.AttrIntensity`,
`model.AttrClassification`, `model.AttrReturnNumber`,
`model.AttrNumberOfReturns`. The LAS/LAZ reader additionally exposes the other
standard point record fields (`gps_time`, `scan_angle`, `point_source_id`,
`user_data`, the classification flag bits, `nir`, ...) and every extra-byte
attribute declared in the file, including common vendor spellings (e.g.
requesting `incidence_angle` matches OPALS `_IncidenceAngle` or GeoCue
`True View Incidence Angle`). Extra bytes declared with a scale and/or offset
are exported as `float64` physical values (`raw*scale+offset`).

Notes:

- Attributes requested but not found in the source (or missing from some
  points) are skipped silently.
- Attribute data types not representable by the selected encoder are omitted
  from that output: 64-bit integers fit neither format, and the GLB encoder
  stores `float64` values (e.g. `gps_time`) as lossy `float32` and drops
  32-bit integers. The PNTS encoder preserves `float64` exactly.
- Attribute names appear uppercased in the output tiles (e.g. `INTENSITY`,
  `GPS_TIME`).

Use an empty attribute set to omit optional attributes:

```go
opts := tiler.NewTilerOptions(
	tiler.WithAttributes(model.NewAttributes()),
)
```

## Mutators

Mutators can transform or discard points after coordinate conversion and before
tree insertion.

### Built-In Mutators

```go
import "github.com/mfbonfigli/gotiler-core/tiler/mutator"

opts := tiler.NewTilerOptions(
	tiler.WithMutators([]mutator.Mutator{
		mutator.NewZOffset(1.5),
	}),
)
```

`NewZOffset` shifts local Z coordinates.

### Custom Mutator

Mutators receive the point in local coordinates plus a typed view over the
point's optional attributes, and return the (possibly modified) point and
whether to keep it. Attribute changes made through the view's setters are
applied in place and flow into the output tiles:

```go
type ClassificationFilter struct {
	Keep uint8
}

func (f ClassificationFilter) Mutate(
	pt model.Point,
	attrs model.AttributeView,
	localToGlobal model.Transform,
) (model.Point, bool) {
	if i := attrs.Index(model.AttrClassification); i >= 0 {
		if v, err := attrs.Value(i); err == nil {
			if c, ok := v.(uint8); ok {
				return pt, c == f.Keep
			}
		}
	}
	return pt, true
}
```

Then pass it through `WithMutators`. Note that mutators only see attributes
that were requested via `WithAttributes`: the filter above requires
`classification` to be part of the requested set.


## Progress Reporting

Progress callbacks receive phase-aware events. Callbacks should return quickly;
dispatch expensive UI or logging work elsewhere.

```go
opts := tiler.NewTilerOptions(
	tiler.WithProgressCallback(func(e tiler.ProgressEvent) {
		if e.Level == tiler.ProgressMilestone {
			log.Printf("[%s] %s", e.Phase.Name, e.Message)
		}
	}),
)
```

The current pipeline reports preparation, tree phases, and exporting.

## Tests

```bash
go test ./...
```

Local `go test ./...` requires a working CGO and PROJ setup. If your local
machine is not configured for PROJ, use the Docker-backed build instead.

## Docker Build And Test

The recommended reproducible path is the Docker-backed build script:

```bash
bash scripts/build.sh linux-amd64
bash scripts/build.sh linux-arm64
bash scripts/build.sh windows-amd64
bash scripts/build.sh all
```

Available targets:

| Target | Behavior |
|--------|----------|
| `linux-amd64` | Builds and runs tests inside Docker |
| `linux-arm64` | Cross-builds Linux ARM64 test binaries and runs them only on a Linux ARM64 host |
| `windows-amd64` | Cross-builds Windows AMD64 test binaries and runs them only on a Windows AMD64 host |
| `all` | Runs every target |

Options:

| Option | Description |
|--------|-------------|
| `--no-cache` | Pass `--no-cache` to `docker build` |
| `--skip-run` | Build artifacts only; do not execute generated test binaries |
| `-h`, `--help` | Show script usage |

Cross-platform targets generate test binaries under `build/tests/<target>` and
copy PROJ data under `build/share/<target>`.

## 🙏 Acknowledgments
* The Go gopher mascot was originally designed by Renée French and is licensed under the Creative Commons 4.0 Attribution License.
