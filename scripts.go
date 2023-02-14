package main

import (
	"bytes"
	"fmt"
	"html/template"
	"os"

	"github.com/PuerkitoBio/goquery"
	"github.com/avelino/awesome-go/pkg/markdown"
)

func readme() []byte {
	input, err := os.ReadFile(readmePath)
	if err != nil {
		panic(err)
	}
	html, err := markdown.ConvertMarkdownToHTML(input)
	if err != nil {
		panic(err)
	}
	return html
}

func startQuery() *goquery.Document {
	buf := bytes.NewBuffer(readme())
	query, err := goquery.NewDocumentFromReader(buf)
	if err != nil {
		panic(err)
	}
	return query
}

type content struct {
	Body template.HTML
}

// GenerateHTML generate site html (index.html) from markdown file
func GenerateHTML(srcFilename, outFilename string) error {
	input, err := os.ReadFile(srcFilename)
	if err != nil {
		return err
	}

	body, err := markdown.ConvertMarkdownToHTML(input)
	if err != nil {
		return err
	}

	c := &content{Body: template.HTML(body)}
	f, err := os.Create(outFilename)
	if err != nil {
		return err
	}

	fmt.Printf("Write Index file: %s\n", outIndexFile)
	if err := tplIndex.Execute(f, c); err != nil {
		return err
	}

	return nil
}
