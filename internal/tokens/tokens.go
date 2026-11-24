// Package tokens counts the tokens of a text, as the embedding model counts
// them. An embedding model has a limit in tokens, not in characters: a
// table has about 2 characters for each token, and a paragraph has about 4.
//
// The count comes from the tokenizers library of Hugging Face, with the
// tokenizer file of the model. "make tokenizer-install" gets the two.
package tokens

import (
	"fmt"
	"strings"

	"github.com/daulet/tokenizers"
)

// Config is the settings of a Counter.
type Config struct {
	// Path is the tokenizer file of the embedding model: its
	// tokenizer.json.
	Path string
}

// Counter counts the tokens of a text. It is safe for use by more than one
// goroutine.
type Counter struct {
	tokenizer *tokenizers.Tokenizer
}

// New reads the tokenizer file. The caller must call Close.
func New(cfg Config) (*Counter, error) {
	tokenizer, err := tokenizers.FromFile(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("read the tokenizer file %s (make tokenizer-install gets it): %w", cfg.Path, err)
	}
	return &Counter{tokenizer: tokenizer}, nil
}

// Count gives the number of tokens of the text. The marks that a model
// adds around a text, such as its end mark, are not in the count.
func (c *Counter) Count(text string) (int, error) {
	// The library reads the text as a C string, which ends at the first
	// zero byte. A zero byte is no text.
	text = strings.ReplaceAll(text, "\x00", "")
	encoding, err := c.tokenizer.EncodeWithOptionsErr(text, false)
	if err != nil {
		return 0, fmt.Errorf("count the tokens of a text: %w", err)
	}
	return len(encoding.IDs), nil
}

// Close frees the memory of the tokenizer.
func (c *Counter) Close() error {
	return c.tokenizer.Close()
}
