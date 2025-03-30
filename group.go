package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"golang.org/x/net/html"
	"labs.lesiw.io/feed/internal/stmt"
)

var reDate = regexp.MustCompile(
	`>([A-Za-z]+ [0-9]+, [0-9]+, [0-9]+:[0-9]+:[0-9]+ [A-Za-z]+)[^A-Za-z]`,
)

type entry struct {
	URL     string
	Subject string
	Group   string
	Body    string
	Date    time.Time
}

func groupFeed(group string) ([]byte, error) {
	slog.Info("groupFeed", "group", group)
	if err := collectEntries(group); err != nil {
		return nil, fmt.Errorf("could not collect entries: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0">
<channel>
<title>%s</title>
`, group))
	rows, err := db.Query(ctx, stmt.GroupEntries)
	if err != nil {
		return nil, fmt.Errorf("could not retrieve collected entries: %w", err)
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByName[entry])
	for _, e := range entries {
		buf.WriteString(fmt.Sprintf(`<item>
<title>%s</title>
<link>%s</link>
<pubDate>%s</pubDate>
<description>%s</description>
</item>
`, e.Subject, e.URL, e.Date.Format(time.RFC822), e.Body))
	}
	if err != nil {
		return nil, fmt.Errorf("could not convert row data: %w", err)
	}
	buf.WriteString(`</channel></rss>
`)
	return buf.Bytes(), nil
}

func collectEntries(group string) (err error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("could not open tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		} else {
			_ = tx.Commit(ctx)
		}
	}()
	url := "https://groups.google.com/g/" + group
	entries, err := findEntries(url, group)
	if err != nil {
		return err
	}
	for _, e := range entries {
		row := tx.QueryRow(ctx, stmt.GroupEntry, e.URL)
		var exists int
		err := row.Scan(&exists)
		if err == nil {
			break // We already have this one.
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("could not retrieve entry: %w", err)
		}
		if err := loadEntry(e.URL, &e); err != nil {
			return fmt.Errorf("bad entry %s: %w", e.URL, err)
		}
		_, err = tx.Exec(ctx, stmt.AddGroupEntry,
			e.URL, e.Group, e.Subject, e.Body, e.Date,
		)
		if err != nil {
			return fmt.Errorf("could not store entry: %w", err)
		}
	}
	return nil
}

func findEntries(url, group string) (entries []entry, err error) {
	n, err := parseHTML(url)
	if err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", url, err)
	}
	for n := range n.Descendants() {
		if attr(n, "role") != "row" {
			continue
		}
		var i int
		for n := range n.Descendants() {
			if attr(n, "role") != "gridcell" {
				continue
			}
			i++
			if i != 3 {
				continue
			}
			var e entry
			e.Group = group
			for n := range n.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				if n.Data != "a" {
					continue
				}
				e.URL = "https://groups.google.com/" +
					strings.TrimPrefix(attr(n, "href"), "./")
				for n := range n.Descendants() {
					if n.FirstChild == nil {
						e.Subject = content(n)
						break
					}
				}
				break
			}
			entries = append(entries, e)
		}
	}
	return
}

func loadEntry(url string, e *entry) error {
	r, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("could not fetch: %w", err)
	}
	defer r.Body.Close()
	page, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("could not read body: %w", err)
	}
	r.Body.Close()
	// Normalize spaces.
	var b bytes.Buffer
	for _, r := range string(page) {
		if unicode.IsSpace(r) {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}
	page = b.Bytes()
	var parseErrs []error

	m := reDate.FindSubmatch(page)
	if len(m) < 2 {
		parseErrs = append(parseErrs, errors.New("could not find entry date"))
		goto body
	}
	e.Date, err = time.Parse("Jan 2, 2006, 3:04:05 PM", string(m[1]))
	if err != nil {
		parseErrs = append(parseErrs, fmt.Errorf(
			"could not parse entry date: %w", err))
	}

body:
	n, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		parseErrs = append(parseErrs, fmt.Errorf(
			"could not parse HTML: %w", err))
		goto end
	}
	for n := range n.Descendants() {
		if attr(n, "role") == "region" {
			// Not sure why this is necessary.
			// Elsewhere, content(node) represents the content inside the tag.
			// But here, it seems to represent the content inside the tag,
			// plus the tag.
			for c := range n.ChildNodes() {
				e.Body += content(c)
			}
			goto end
		}
	}
	parseErrs = append(parseErrs, errors.New("could not find entry body"))
end:
	return errors.Join(parseErrs...)
}
