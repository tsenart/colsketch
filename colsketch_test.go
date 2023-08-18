package colsketch

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func getWikiWords() ([]string, error) {
	resp, err := http.Get("https://www.corpusdata.org/wiki/samples/text.zip")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d fetching zip file", resp.StatusCode)
	}

	// Read the full response into memory.
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	zipReader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	// Assuming there's only one file in the zip. Adjust as necessary.
	if len(zipReader.File) != 1 {
		return nil, fmt.Errorf("unexpected number of files in zip: %d", len(zipReader.File))
	}

	rc, err := zipReader.File[0].Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	contents, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	// Split the contents into words
	words := strings.Fields(string(contents))

	return words, nil
}

func TestDictionary(t *testing.T) {
	words, err := getWikiWords()
	if err != nil {
		t.Fatalf("failed to get wiki words: %v", err)
	}

	began := time.Now()
	// Assuming the Dict type and constructor exists
	dict := NewDict(Byte, words)
	if dict.Len() == 0 {
		t.Errorf("Failed to produce any dictionary codes")
	}

	t.Logf("Dictionary construction took %v with %d codes", time.Since(began), dict.Len())

	for i, val := range dict.codes {
		t.Logf("code 0x%04x = %v\n", 2*(i+1), val)
	}

	queryWords := []string{"", "and", "ape", "the", "thorn", "yolo", "zygote"}
	for _, word := range queryWords {
		code := dict.Encode(word)
		t.Logf("query: %s => code 0x%04x\n", word, code)
	}
}
