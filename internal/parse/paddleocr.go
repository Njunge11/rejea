package parse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is the settings of the PaddleOCR adapter.
type Config struct {
	// Name is the name and the version of the model that the parser
	// service runs. The service does not tell its version, so the person
	// who installs it gives the name.
	Name string
	// URL is the address of the parser service, such as
	// http://127.0.0.1:8112.
	URL string
	// Timeout is the longest time that the parser gets for one page.
	Timeout time.Duration
	// MaxAnswerBytes is the largest answer that the adapter reads. The
	// answer is in memory as a whole.
	MaxAnswerBytes int64
	// ReadCharts makes the parser read the values of each chart. A chart
	// then takes some seconds more. If not, a chart has no text.
	ReadCharts bool
	// ReadImageText makes the parser read the text in each picture, such
	// as the numbers in an infographic. A picture then takes some seconds
	// more. If not, a picture has no text.
	ReadImageText bool
}

// PaddleOCR is the adapter for the parser service of this repository: the
// layout parsing API of PaddleX with the model PaddleOCR-VL. It is safe for
// use by many goroutines.
type PaddleOCR struct {
	endpoint string
	health   string
	cfg      Config
	client   *http.Client
}

// NewPaddleOCR makes a PaddleOCR. It does not call the parser service.
func NewPaddleOCR(cfg Config) (*PaddleOCR, error) {
	base, err := url.Parse(cfg.URL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("the URL of the parser is %q: it must be an http or https URL with a host", cfg.URL)
	}
	if strings.TrimSpace(cfg.Name) == "" {
		return nil, errors.New("the parser has no name")
	}
	if cfg.Timeout <= 0 || cfg.MaxAnswerBytes < 1 {
		return nil, fmt.Errorf("the parser has a time limit of %s and an answer limit of %d bytes: each must be more than zero",
			cfg.Timeout, cfg.MaxAnswerBytes)
	}
	return &PaddleOCR{
		endpoint: base.JoinPath("layout-parsing").String(),
		health:   base.JoinPath("health").String(),
		cfg:      cfg,
		// The time limit of a call is the limit of its context.
		client: &http.Client{},
	}, nil
}

// Name gives the name and the version of the model of the parser service.
func (p *PaddleOCR) Name() string { return p.cfg.Name }

// Ready confirms that the parser service answers. The caller gives the
// time limit.
func (p *PaddleOCR) Ready(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.health, nil)
	if err != nil {
		return fmt.Errorf("make the request for the parser: %w", err)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("call the parser: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck // The status is the answer.
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("the parser answers %s", response.Status)
	}
	return nil
}

// paddleRequest is the body of a request to the layout parsing API.
type paddleRequest struct {
	// File is the image. The JSON encoder writes it in base64.
	File []byte `json:"file"`
	// FileType 1 is an image. 0 is a PDF.
	FileType int `json:"fileType"`
	// The service can also return pictures of its result and the pictures
	// of the page. The adapter uses the blocks only.
	Visualize            bool `json:"visualize"`
	ReturnMarkdownImages bool `json:"returnMarkdownImages"`
	UseChartRecognition  bool `json:"useChartRecognition"`
	UseOcrForImageBlock  bool `json:"useOcrForImageBlock"`
}

// paddleAnswer is the body of an answer. An answer with an error has the
// error code and the message only.
type paddleAnswer struct {
	ErrorCode int    `json:"errorCode"`
	ErrorMsg  string `json:"errorMsg"`
	Result    struct {
		Pages []struct {
			Result struct {
				// Width and Height are the size of the image in pixels.
				Width  float64 `json:"width"`
				Height float64 `json:"height"`
				// Blocks are in reading order.
				Blocks []struct {
					Label   string `json:"block_label"`
					Content string `json:"block_content"`
					// Box is left, top, right, bottom in pixels.
					Box []float64 `json:"block_bbox"`
				} `json:"parsing_res_list"`
			} `json:"prunedResult"`
		} `json:"layoutParsingResults"`
	} `json:"result"`
}

// paddleTypes gives the type of a block for each label of the layout model
// PP-DocLayoutV3. The list is the label_list of the model.
var paddleTypes = map[string]Type{
	"doc_title":         Title,
	"paragraph_title":   Heading,
	"text":              Paragraph,
	"vertical_text":     Paragraph,
	"abstract":          Paragraph,
	"content":           Paragraph, // a table of contents
	"reference":         Paragraph,
	"reference_content": Paragraph,
	"algorithm":         Paragraph,
	"table":             Table,
	"image":             Figure,
	"chart":             Figure,
	"seal":              Figure,
	"figure_title":      Caption,
	"display_formula":   Formula,
	"inline_formula":    Formula,
	"formula_number":    Formula,
	"footnote":          Footnote,
	"vision_footnote":   Footnote,
	"header":            Header,
	"header_image":      Header,
	"footer":            Footer,
	"footer_image":      Footer,
	"number":            PageNumber,
	"aside_text":        Aside,
}

// Parse sends the image to the parser service and gives its blocks.
//
// Each error is one that a new attempt can correct: the service is not
// there, it is too slow, or it failed. The page images come from the
// service itself, so the parser has no reason to refuse one.
func (p *PaddleOCR) Parse(ctx context.Context, image []byte) ([]Block, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	body, err := json.Marshal(paddleRequest{
		File: image, FileType: 1, UseChartRecognition: p.cfg.ReadCharts, UseOcrForImageBlock: p.cfg.ReadImageText,
	})
	if err != nil {
		return nil, fmt.Errorf("make the request for the parser: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("make the request for the parser: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call the parser: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck // The read of the body found each error.

	// One byte more than the limit tells that the answer is too large.
	raw, err := io.ReadAll(io.LimitReader(response.Body, p.cfg.MaxAnswerBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the answer of the parser: %w", err)
	}
	if int64(len(raw)) > p.cfg.MaxAnswerBytes {
		return nil, fmt.Errorf("the answer of the parser has more than %d bytes", p.cfg.MaxAnswerBytes)
	}
	var answer paddleAnswer
	decodeErr := json.Unmarshal(raw, &answer)
	if response.StatusCode != http.StatusOK {
		// The service gives its reason in the answer. If the answer is not
		// from the service, the status is all that the adapter knows.
		if decodeErr != nil || answer.ErrorMsg == "" {
			return nil, fmt.Errorf("the parser answered with the status %d", response.StatusCode)
		}
		return nil, fmt.Errorf("the parser answered with the status %d: %s", response.StatusCode, answer.ErrorMsg)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("read the answer of the parser: %w", decodeErr)
	}
	if len(answer.Result.Pages) != 1 {
		return nil, fmt.Errorf("the parser gave %d pages for one image", len(answer.Result.Pages))
	}
	page := answer.Result.Pages[0].Result
	if page.Width <= 0 || page.Height <= 0 {
		return nil, fmt.Errorf("the parser gave an image size of %g x %g", page.Width, page.Height)
	}
	blocks := make([]Block, len(page.Blocks))
	for i, block := range page.Blocks {
		blockType, known := paddleTypes[block.Label]
		if !known {
			// A new version of the layout model has a new label. The
			// adapter must learn its type before it reads such a page.
			return nil, fmt.Errorf("the parser gave a block with the label %q, which the adapter does not know", block.Label)
		}
		if len(block.Box) != 4 {
			return nil, errors.New("the parser gave a block with no box")
		}
		text := block.Content
		if blockType == Table {
			// The parser writes a new line in a cell as the two
			// characters backslash and n.
			text = strings.ReplaceAll(text, `\n`, "<br>")
		}
		blocks[i] = Block{
			Type: blockType,
			Text: text,
			Box: Box{
				Left:   fraction(block.Box[0], page.Width),
				Top:    fraction(block.Box[1], page.Height),
				Right:  fraction(block.Box[2], page.Width),
				Bottom: fraction(block.Box[3], page.Height),
			},
		}
	}
	return blocks, nil
}

// fraction gives a place in pixels as a fraction of the size of the image,
// from 0 to 1. The layout model can give a place that is a little out of
// the image.
func fraction(pixels, size float64) float64 {
	return min(max(pixels/size, 0), 1)
}
