// Package pdf reads PDF files with PDFium: the number of pages, and an
// image of each page.
//
// PDFium is a C++ library. This package runs the WebAssembly build of
// PDFium that the module go-pdfium has in it, on the wazero runtime. Thus
// the service needs no C compiler and no PDFium library on the machine.
// PDFium also has no access to the machine: a PDF that breaks PDFium
// cannot break the service or read a file.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/structs"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// white is the colour of the paper: PDFium draws each page on it.
const white = 0xFFFFFFFF

// pointsPerInch is the number of points in one inch. A PDF gives the size
// of a page in points.
const pointsPerInch = 72

// Config is the settings of the page images.
type Config struct {
	// DPI is the resolution of a page image: the number of pixels for
	// each inch of the page.
	DPI int
	// MaxPagePixels is the largest number of pixels in one page image.
	// One pixel uses 4 bytes of memory. A page that is larger at the DPI
	// gets a lower resolution, so that its image has this number.
	MaxPagePixels int
	// MaxOpen is the largest number of PDFs that are open at the same
	// time. Each open PDF has its own copy of PDFium.
	MaxOpen int
}

// ContentError is a fault in the content of a PDF. A new attempt reads the
// same file and finds the same fault.
type ContentError struct{ Reason string }

func (e *ContentError) Error() string { return e.Reason }

// IsContentError tells if an error is a ContentError.
func IsContentError(err error) bool {
	_, ok := errors.AsType[*ContentError](err)
	return ok
}

// Reader opens PDF files. It is safe for use by many goroutines.
type Reader struct {
	pool pdfium.Pool
	cfg  Config
}

// New makes a Reader. It compiles PDFium for the processor of the machine,
// which takes about one second. PDFium logs through logger.
func New(logger *slog.Logger, cfg Config) (*Reader, error) {
	if cfg.DPI < 1 || cfg.MaxPagePixels < 1 || cfg.MaxOpen < 1 {
		return nil, fmt.Errorf("the PDF settings are DPI %d, %d pixels in a page and %d open PDFs: each must be 1 or more",
			cfg.DPI, cfg.MaxPagePixels, cfg.MaxOpen)
	}
	output := &logWriter{logger: logger}
	pool, err := webassembly.Init(webassembly.Config{
		// wazero compiles on one processor core when the context does not
		// tell it a number.
		Context:  experimental.WithCompilationWorkers(context.Background(), runtime.GOMAXPROCS(0)),
		MaxTotal: cfg.MaxOpen,
		// PDFium sees no folder of the machine. A PDF with a font that is
		// not in the file thus gets the fonts that PDFium has in it, which
		// are the same on each machine.
		FSConfig: wazero.NewFSConfig(),
		// The first two features are the ones that go-pdfium sets. The
		// third lets Kill stop a call that is in work: without it, a PDF
		// that makes PDFium run with no end keeps its worker for ever.
		RuntimeConfig: wazero.NewRuntimeConfig().
			WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
			WithCloseOnContextDone(true),
		Stdout: output,
		Stderr: output,
	})
	if err != nil {
		return nil, fmt.Errorf("start PDFium: %w", err)
	}
	// The compile used about 300 MB of memory that is now free. Go gives
	// free memory back to the system over some minutes. This call gives it
	// back at once, so the first jobs do not add to it.
	debug.FreeOSMemory()
	return &Reader{pool: pool, cfg: cfg}, nil
}

// Close stops PDFium. Each Document must be closed before it.
func (r *Reader) Close() error {
	return r.pool.Close()
}

// logWriter sends the text that PDFium prints to the log.
type logWriter struct{ logger *slog.Logger }

func (w *logWriter) Write(p []byte) (int, error) {
	if text := strings.TrimSpace(string(p)); text != "" {
		w.logger.Debug("pdfium: " + text)
	}
	return len(p), nil
}

// Document is one open PDF. One goroutine uses it, and closes it.
type Document struct {
	cfg      Config
	ctx      context.Context //nolint:containedctx // PDFium has no context argument.
	instance pdfium.Pdfium
	ref      references.FPDF_DOCUMENT
	// form is the form of the PDF. PDFium needs it to draw the fields of
	// a form, also for a PDF that has no form.
	form  references.FPDF_FORMHANDLE
	file  *watchedFile
	pages int
	// stop removes the kill that the end of ctx starts. killed is closed
	// when that kill is complete.
	stop   func() bool
	killed chan struct{}
}

// Open opens a PDF of size bytes. PDFium reads the parts of the file that
// it needs, when it needs them, so a large file is never in memory as a
// whole. The caller closes the Document.
//
// When ctx ends, the work of PDFium on the Document stops, also in the
// middle of a call, and each later call fails with the cause of the end.
//
// The error is a ContentError when the file is not a PDF that PDFium can
// read, when it needs a password, and when it has no page. An error from
// file comes back as it is.
func (r *Reader) Open(ctx context.Context, file io.ReaderAt, size int64) (*Document, error) {
	instance, err := r.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("get a copy of PDFium: %w", err)
	}
	d := &Document{
		cfg:      r.cfg,
		ctx:      ctx,
		instance: instance,
		file:     &watchedFile{file: file},
		killed:   make(chan struct{}),
	}
	d.stop = context.AfterFunc(ctx, func() {
		defer close(d.killed)
		instance.Kill() //nolint:errcheck,gosec // The copy of PDFium is of no more use.
	})

	opened, err := instance.OpenDocument(&requests.OpenDocument{
		FileReader:     io.NewSectionReader(d.file, 0, size),
		FileReaderSize: size,
	})
	if err != nil {
		d.Close()
		err = d.cause(err)
		switch {
		case errors.Is(err, pdfiumerrors.ErrPassword):
			return nil, &ContentError{"the PDF needs a password"}
		case errors.Is(err, pdfiumerrors.ErrSecurity):
			return nil, &ContentError{"the PDF has an encryption that the service cannot read"}
		case errors.Is(err, pdfiumerrors.ErrFormat):
			return nil, &ContentError{"the file is not a PDF that the service can read"}
		}
		return nil, fmt.Errorf("open the PDF: %w", err)
	}
	d.ref = opened.Document

	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: d.ref})
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("count the pages of the PDF: %w", d.cause(err))
	}
	if count.PageCount < 1 {
		d.Close()
		return nil, &ContentError{"the PDF has no page"}
	}
	d.pages = count.PageCount

	form, err := instance.FPDFDOC_InitFormFillEnvironment(&requests.FPDFDOC_InitFormFillEnvironment{
		Document: d.ref, FormFillInfo: noFormEvents,
	})
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("read the form of the PDF: %w", d.cause(err))
	}
	d.form = form.FormHandle
	return d, nil
}

// Pages gives the number of pages.
func (d *Document) Pages() int { return d.pages }

// Close gives the memory of the PDF back. The copy of PDFium goes with it,
// because WebAssembly cannot give memory back in a different way.
func (d *Document) Close() {
	if !d.stop() {
		// The end of ctx started the kill, which closes the copy.
		<-d.killed
		return
	}
	if d.form != "" {
		// go-pdfium keeps each form in a list of the package, with the
		// copy of PDFium that made it. Only this call removes the form
		// from that list: without it, the memory of each opened PDF
		// stays for the life of the service. A copy that the end of ctx
		// killed cannot get this call.
		d.instance.FPDFDOC_ExitFormFillEnvironment(&requests.FPDFDOC_ExitFormFillEnvironment{FormHandle: d.form}) //nolint:errcheck,gosec // The close of the copy frees the form too.
	}
	d.instance.Close() //nolint:errcheck,gosec // The copy of PDFium is of no more use.
}

// cause gives the real cause of an error from PDFium. PDFium reports a
// read that failed as a damaged file, and a kill as a closed module.
func (d *Document) cause(err error) error {
	if d.ctx.Err() != nil {
		return context.Cause(d.ctx)
	}
	if d.file.err != nil {
		return d.file.err
	}
	return err
}

// watchedFile keeps the first error of the reads of a file.
type watchedFile struct {
	file io.ReaderAt
	err  error
}

func (w *watchedFile) ReadAt(p []byte, offset int64) (int, error) {
	n, err := w.file.ReadAt(p, offset)
	if err != nil && !errors.Is(err, io.EOF) && w.err == nil {
		w.err = err
	}
	return n, err
}

// Image is the image of one page.
type Image struct {
	// PNG is the image as a PNG file.
	PNG []byte
	// Width and Height are the size of the image in pixels.
	Width, Height int
}

// Render makes the image of one page. The first page has the number 1.
//
// The image shows the page as a PDF viewer shows it: on white paper, with
// its annotations and with the values in the fields of a form.
func (d *Document) Render(number int) (Image, error) {
	img, err := d.render(number)
	if err != nil {
		return Image{}, fmt.Errorf("render page %d of the PDF: %w", number, d.cause(err))
	}
	return img, nil
}

// render draws the page with the bitmap functions of PDFium. The render
// functions of go-pdfium are shorter, but they draw a page that has
// transparency on a clear background, and the colours of such a page are
// then different from the colours on white paper.
func (d *Document) render(number int) (Image, error) {
	loaded, err := d.instance.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: d.ref, Index: number - 1})
	if err != nil {
		return Image{}, err
	}
	page := requests.Page{ByReference: &loaded.Page}
	defer d.instance.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: loaded.Page}) //nolint:errcheck // Close frees the page too.

	// PDFium makes the fields of the form of a page between these two
	// calls.
	_, err = d.instance.FORM_OnAfterLoadPage(&requests.FORM_OnAfterLoadPage{Page: page, FormHandle: d.form})
	if err != nil {
		return Image{}, err
	}
	defer d.instance.FORM_OnBeforeClosePage(&requests.FORM_OnBeforeClosePage{Page: page, FormHandle: d.form}) //nolint:errcheck // Close frees the fields too.

	size, err := d.instance.GetPageSize(&requests.GetPageSize{Page: page})
	if err != nil {
		return Image{}, err
	}
	width, height := pixels(size.Width, size.Height, d.cfg.DPI, d.cfg.MaxPagePixels)

	// Four bytes for each pixel. The fourth is not in use: a page on white
	// paper has no part that the eye can see through.
	created, err := d.instance.FPDFBitmap_Create(&requests.FPDFBitmap_Create{Width: width, Height: height})
	if err != nil {
		return Image{}, err
	}
	bitmap := created.Bitmap
	defer d.instance.FPDFBitmap_Destroy(&requests.FPDFBitmap_Destroy{Bitmap: bitmap}) //nolint:errcheck // Close frees the bitmap too.

	_, err = d.instance.FPDFBitmap_FillRect(&requests.FPDFBitmap_FillRect{
		Bitmap: bitmap, Width: width, Height: height, Color: white,
	})
	if err != nil {
		return Image{}, err
	}
	// PDFium writes blue, green, red. The second flag makes it write red,
	// green, blue, which is the order of a Go image.
	const flags = enums.FPDF_RENDER_FLAG_ANNOT | enums.FPDF_RENDER_FLAG_REVERSE_BYTE_ORDER
	_, err = d.instance.FPDF_RenderPageBitmap(&requests.FPDF_RenderPageBitmap{
		Bitmap: bitmap, Page: page, SizeX: width, SizeY: height, Flags: flags,
	})
	if err != nil {
		return Image{}, err
	}
	// The call above draws each annotation but the fields of a form.
	_, err = d.instance.FPDF_FFLDraw(&requests.FPDF_FFLDraw{
		FormHandle: d.form, Bitmap: bitmap, Page: page, SizeX: width, SizeY: height, Flags: flags,
	})
	if err != nil {
		return Image{}, err
	}
	stride, err := d.instance.FPDFBitmap_GetStride(&requests.FPDFBitmap_GetStride{Bitmap: bitmap})
	if err != nil {
		return Image{}, err
	}
	// The buffer is the memory of PDFium, not a copy. It is good until
	// the bitmap is destroyed.
	buffer, err := d.instance.FPDFBitmap_GetBuffer(&requests.FPDFBitmap_GetBuffer{Bitmap: bitmap})
	if err != nil {
		return Image{}, err
	}
	pix := buffer.Buffer
	if stride.Stride < 4*width || len(pix) < stride.Stride*height {
		return Image{}, fmt.Errorf("PDFium gave a bitmap of %d bytes with %d in a row for an image of %d x %d",
			len(pix), stride.Stride, width, height)
	}
	// The PNG encoder reads the fourth byte as the opacity.
	for i := 3; i < len(pix); i += 4 {
		pix[i] = 0xFF
	}
	var file bytes.Buffer
	err = png.Encode(&file, &image.RGBA{Pix: pix, Stride: stride.Stride, Rect: image.Rect(0, 0, width, height)})
	if err != nil {
		return Image{}, err
	}
	return Image{PNG: file.Bytes(), Width: width, Height: height}, nil
}

// noFormEvents is the events of a form that PDFium must have an answer
// for. PDFium sends them when a person fills in a form on a screen. The
// service only draws the form, so each answer is empty.
var noFormEvents = structs.FPDF_FORMFILLINFO{
	FFI_Invalidate:         func(references.FPDF_PAGE, float64, float64, float64, float64) {},
	FFI_SetCursor:          func(enums.FXCT) {},
	FFI_SetTimer:           func(int, func(int)) int { return 0 },
	FFI_KillTimer:          func(int) {},
	FFI_GetLocalTime:       func() structs.FPDF_SYSTEMTIME { return structs.FPDF_SYSTEMTIME{} },
	FFI_GetPage:            func(references.FPDF_DOCUMENT, int) *references.FPDF_PAGE { return nil },
	FFI_GetRotation:        func(references.FPDF_PAGE) enums.FPDF_PAGE_ROTATION { return enums.FPDF_PAGE_ROTATION_NONE },
	FFI_ExecuteNamedAction: func(string) {},
}

// pixels gives the size of the image of a page. The page has its size in
// points. A part of a pixel at an edge becomes a full pixel, so the image
// has all of the page.
//
// When the image has more pixels than the limit at the DPI, pixels gives
// the largest size of the same shape that the limit permits.
func pixels(widthPoints, heightPoints float64, dpi, maxPixels int) (width, height int) {
	scale := float64(dpi) / pointsPerInch
	width = int(math.Ceil(widthPoints * scale))
	height = int(math.Ceil(heightPoints * scale))
	if width*height <= maxPixels {
		return width, height
	}
	scale = math.Sqrt(float64(maxPixels) / (widthPoints * heightPoints))
	return max(int(widthPoints*scale), 1), max(int(heightPoints*scale), 1)
}
