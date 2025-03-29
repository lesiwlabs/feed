package main

import (
	"bytes"
	"fmt"
	"net/http"

	"golang.org/x/net/html"
)

func parseHTML(url string) (*html.Node, error) {
	res, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", url, err)
	}
	defer res.Body.Close()
	return html.Parse(res.Body)
}

func attr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func content(n *html.Node) string {
	var buf bytes.Buffer
	err := html.Render(&buf, n)
	if err != nil {
		return ""
	}
	return buf.String()
}
