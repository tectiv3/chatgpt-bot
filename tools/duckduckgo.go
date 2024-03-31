package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	safe "github.com/eminarican/safetypes"
)

type SearchParam struct {
	Query     string
	Region    string
	ImageType string
}

type ClientOption struct {
	Referrer  string
	UserAgent string
	Timeout   time.Duration
}

type SearchResult struct {
	Title   string
	Link    string
	Snippet string
	Image   string
}

type Result struct {
	Answer  string `json:"Answer"`
	Results []struct {
		Height    int    `json:"height"`
		Image     string `json:"image"`
		Source    string `json:"source"`
		Thumbnail string `json:"thumbnail"`
		Title     string `json:"title"`
		URL       string `json:"url"`
		Width     int    `json:"width"`
	} `json:"results"`
}

var defaultClientOption = &ClientOption{
	Referrer:  "https://duckduckgo.com",
	UserAgent: `Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36`,
	Timeout:   5 * time.Second,
}

func NewClientOption(referrer, userAgent string, timeout time.Duration) *ClientOption {
	if referrer == "" {
		referrer = defaultClientOption.Referrer
	}
	if userAgent == "" {
		referrer = defaultClientOption.UserAgent
	}

	if timeout == 0 {
		timeout = defaultClientOption.Timeout
	}

	return &ClientOption{
		Referrer:  referrer,
		UserAgent: userAgent,
		Timeout:   timeout,
	}
}

func NewSearchParam(query, region string) (*SearchParam, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("search query is empty")
	}

	return &SearchParam{Query: q, Region: region}, nil
}

func NewSearchImageParam(query, region, imageType string) (*SearchParam, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("search query is empty")
	}

	return &SearchParam{
		Query:     q,
		Region:    region,
		ImageType: imageType,
	}, nil
}

func (param *SearchParam) buildURL() (*url.URL, error) {
	u := &url.URL{
		Scheme: "https",
		Host:   "html.duckduckgo.com",
		Path:   "html",
	}
	q := u.Query()
	q.Add("q", param.Query)
	q.Add("l", param.Region)
	q.Add("v", "1")
	q.Add("o", "json")
	q.Add("api", "/d.js")
	u.RawQuery = q.Encode()

	return u, nil
}

func buildRequest(param *SearchParam, opt *ClientOption) (*http.Request, error) {
	u, err := param.buildURL()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return req, err
	}

	req.Header.Add("Referrer", opt.Referrer)
	req.Header.Add("User-Agent", opt.UserAgent)
	req.Header.Add("Cookie", "kl="+param.Region)
	req.Header.Add("Content-Type", "x-www-form-urlencoded")

	return req, nil
}

var re = regexp.MustCompile(`vqd="([\d-]+)"`)

func addParams(r *http.Request, p map[string]string) {
	q := r.URL.Query()

	for k, v := range p {
		q.Add(k, v)
	}

	r.URL.RawQuery = q.Encode()
}

func token(keywords string) (string, error) {
	var client = http.DefaultClient
	const URL = "https://duckduckgo.com/"

	r, _ := http.NewRequest("POST", URL, nil)
	addParams(r, map[string]string{"q": keywords})

	res, err := client.Do(r)
	if err != nil {
		return "", err
	}

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)

	if err != nil {
		return "", err
	}

	token := re.Find(body)

	if token == nil {
		log.Println(string(body))
		return "", errors.New("token parsing failed")
	}

	return strings.Trim(string(token)[4:len(token)-1], "\"&"), nil
}

func buildImagesRequest(param *SearchParam, opt *ClientOption) (*http.Request, error) {
	vqd, err := token(param.Query)
	if err != nil {
		return nil, err
	}
	log.Printf("vqd: %s", vqd)
	u := &url.URL{
		Scheme: "https",
		Host:   "duckduckgo.com",
		Path:   "i.js",
	}

	q := u.Query()
	q.Add("l", param.Region)
	q.Add("o", "json")
	q.Add("q", param.Query)
	//q.Add("v", "1")
	q.Add("vqd", vqd)
	q.Add("f", ",,,type:"+param.ImageType)
	q.Add("p", "-1")
	q.Add("s", "0")
	//q.Add("v7exp", "a")
	//q.Add("api", "/i.js")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return req, err
	}

	req.Header.Add("Referrer", opt.Referrer)
	req.Header.Add("User-Agent", opt.UserAgent)
	req.Header.Add("Cookie", "kl="+param.Region)
	req.Header.Add("Content-Type", "x-www-form-urlencoded")

	return req, nil
}

func parse(r io.Reader) safe.Result[*[]SearchResult] {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return safe.Err[*[]SearchResult](err.Error())
	}

	var (
		result []SearchResult
		item   SearchResult
	)
	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		item.Title = s.Find(".result__title a").Text()

		item.Link = extractLink(
			s.Find(".result__url").AttrOr("href", ""),
		)

		item.Snippet = removeHtmlTagsFromText(
			s.Find(".result__snippet").Text(),
		)

		result = append(result, item)
	})

	return safe.AsResult[*[]SearchResult](&result, nil)
}

func removeHtmlTags(node *html.Node, buf *bytes.Buffer) {
	if node.Type == html.TextNode {
		buf.WriteString(node.Data)
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		removeHtmlTags(child, buf)
	}
}

func removeHtmlTagsFromText(text string) string {
	node, err := html.Parse(strings.NewReader(text))
	if err != nil {
		// If it cannot be parsed text as HTML, return the text as is.
		return text
	}

	buf := &bytes.Buffer{}
	removeHtmlTags(node, buf)

	return buf.String()
}

// Extract target URL from href included in search result
// e.g.
//
//	`//duckduckgo.com/l/?uddg=https%3A%2F%2Fwww.vim8.org%2Fdownload.php&amp;rut=...`
//	                          ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
//	                     --> `https://www.vim8.org/download.php`
func extractLink(href string) string {
	u, err := url.Parse(fmt.Sprintf("https:%s", strings.TrimSpace(href)))
	if err != nil {
		return ""
	}

	q := u.Query()
	if !q.Has("uddg") {
		return ""
	}

	return q.Get("uddg")
}

func SearchWithOption(param *SearchParam, opt *ClientOption) safe.Result[*[]SearchResult] {
	c := &http.Client{
		Timeout: opt.Timeout,
	}
	req, err := buildRequest(param, opt)
	if err != nil {
		return safe.Err[*[]SearchResult](err.Error())
	}

	resp, err := c.Do(req)
	if err != nil {
		return safe.Err[*[]SearchResult](err.Error())
	}
	defer resp.Body.Close()

	result := parse(resp.Body)
	if result.IsErr() {
		return result
	}

	return result
}

func Search(param *SearchParam) safe.Result[*[]SearchResult] {
	return SearchWithOption(param, defaultClientOption)
}

func SearchImages(param *SearchParam) safe.Result[*[]SearchResult] {
	c := &http.Client{Timeout: defaultClientOption.Timeout}
	req, err := buildImagesRequest(param, defaultClientOption)
	if err != nil {
		return safe.Err[*[]SearchResult](err.Error())
	}

	resp, err := c.Do(req)
	if err != nil {
		return safe.Err[*[]SearchResult](err.Error())
	}
	defer resp.Body.Close()

	result := Result{}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return safe.Err[*[]SearchResult](err.Error())
	}
	if err := json.Unmarshal(body, &result); err != nil {
		log.Println(string(body))
		return safe.Err[*[]SearchResult](string(body))
	}
	var (
		res  []SearchResult
		item SearchResult
	)
	for _, r := range result.Results {
		item.Title = r.Title
		item.Link = r.URL
		item.Snippet = ""
		item.Image = r.Image
		res = append(res, item)
	}

	rand.Shuffle(len(res), func(i, j int) { res[i], res[j] = res[j], res[i] })

	return safe.AsResult[*[]SearchResult](&res, nil)
}
