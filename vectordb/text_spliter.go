package vectordb

// TextSplitter is the standard interface for splitting texts.
type TextSplitter interface {
	SplitText(text string) ([]string, error)
}
