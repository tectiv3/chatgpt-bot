package vectordb

import (
	"bytes"
	"context"
	"io"
)

// Text loads text data from an io.Reader.
type Text struct {
	r io.Reader
}

// Loader is the interface for loading and splitting documents from a source.
type Loader interface {
	// Load loads from a source and returns documents.
	Load(ctx context.Context) ([]Document, error)
	// LoadAndSplit loads from a source and splits the documents using a text splitter.
	LoadAndSplit(ctx context.Context, splitter TextSplitter) ([]Document, error)
}

var _ Loader = Text{}

// NewText creates a new text loader with an io.Reader.
func NewText(r io.Reader) Text {
	return Text{
		r: r,
	}
}

// Load reads from the io.Reader and returns a single document with the data.
func (l Text) Load(_ context.Context) ([]Document, error) {
	buf := new(bytes.Buffer)
	_, err := io.Copy(buf, l.r)
	if err != nil {
		return nil, err
	}

	return []Document{
		{
			PageContent: buf.String(),
			Metadata:    map[string]any{},
		},
	}, nil
}

// LoadAndSplit reads text data from the io.Reader and splits it into multiple
// documents using a text splitter.
func (l Text) LoadAndSplit(ctx context.Context, splitter TextSplitter) ([]Document, error) {
	docs, err := l.Load(ctx)
	if err != nil {
		return nil, err
	}

	return SplitDocuments(splitter, docs)
}
