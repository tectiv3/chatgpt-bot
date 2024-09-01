package vectordb

import "unicode/utf8"

// TsOptions is a struct that contains options for a text splitter.
type TsOptions struct {
	ChunkSize         int
	ChunkOverlap      int
	Separators        []string
	LenFunc           func(string) int
	ModelName         string
	EncodingName      string
	AllowedSpecial    []string
	DisallowedSpecial []string
	SecondSplitter    TextSplitter
	CodeBlocks        bool
	ReferenceLinks    bool
}

// DefaultTsOptions returns the default options for all text splitter.
func DefaultTsOptions() TsOptions {
	return TsOptions{
		ChunkSize:    _defaultTokenChunkSize,
		ChunkOverlap: _defaultTokenChunkOverlap,
		Separators:   []string{"\n\n", "\n", " ", ""},
		LenFunc:      utf8.RuneCountInString,

		ModelName:         _defaultTokenModelName,
		EncodingName:      _defaultTokenEncoding,
		AllowedSpecial:    []string{},
		DisallowedSpecial: []string{"all"},
	}
}

// TsOption is a function that can be used to set options for a text splitter.
type TsOption func(*TsOptions)

// WithChunkSize sets the chunk size for a text splitter.
func WithChunkSize(chunkSize int) TsOption {
	return func(o *TsOptions) {
		o.ChunkSize = chunkSize
	}
}

// WithChunkOverlap sets the chunk overlap for a text splitter.
func WithChunkOverlap(chunkOverlap int) TsOption {
	return func(o *TsOptions) {
		o.ChunkOverlap = chunkOverlap
	}
}

// WithSeparators sets the separators for a text splitter.
func WithSeparators(separators []string) TsOption {
	return func(o *TsOptions) {
		o.Separators = separators
	}
}

// WithLenFunc sets the lenfunc for a text splitter.
func WithLenFunc(lenFunc func(string) int) TsOption {
	return func(o *TsOptions) {
		o.LenFunc = lenFunc
	}
}

// WithModelName sets the model name for a text splitter.
func WithModelName(modelName string) TsOption {
	return func(o *TsOptions) {
		o.ModelName = modelName
	}
}

// WithEncodingName sets the encoding name for a text splitter.
func WithEncodingName(encodingName string) TsOption {
	return func(o *TsOptions) {
		o.EncodingName = encodingName
	}
}

// WithAllowedSpecial sets the allowed special tokens for a text splitter.
func WithAllowedSpecial(allowedSpecial []string) TsOption {
	return func(o *TsOptions) {
		o.AllowedSpecial = allowedSpecial
	}
}

// WithDisallowedSpecial sets the disallowed special tokens for a text splitter.
func WithDisallowedSpecial(disallowedSpecial []string) TsOption {
	return func(o *TsOptions) {
		o.DisallowedSpecial = disallowedSpecial
	}
}

// WithSecondSplitter sets the second splitter for a text splitter.
func WithSecondSplitter(secondSplitter TextSplitter) TsOption {
	return func(o *TsOptions) {
		o.SecondSplitter = secondSplitter
	}
}

// WithCodeBlocks sets whether indented and fenced codeblocks should be included
// in the output.
func WithCodeBlocks(renderCode bool) TsOption {
	return func(o *TsOptions) {
		o.CodeBlocks = renderCode
	}
}

// WithReferenceLinks sets whether reference links (i.e. `[text][label]`)
// should be patched with the url and title from their definition. Note that
// by default reference definitions are dropped from the output.
//
// Caution: this also affects how other inline elements are rendered, e.g. all
// emphasis will use `*` even when another character (e.g. `_`) was used in the
// input.
func WithReferenceLinks(referenceLinks bool) TsOption {
	return func(o *TsOptions) {
		o.ReferenceLinks = referenceLinks
	}
}
