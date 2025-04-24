package model

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
)

type WordList struct {
	Adjectives Words `json:"adjectives"`
	Nouns      Words `json:"nouns"`
}

type Words struct {
	Count int      `json:"count"`
	Words []string `json:"words"`
}

var projectName string
var err error

func (w *WordList) Getwords() string {
	var projectName string

	projectName = w.getNoun() + "-" + w.getAdjective()

	return projectName
}

func (w *WordList) AddWord(category, word string) error {
	if category == "nouns" {
		index := sort.Search(w.Nouns.Count, func(i int) bool {
			return w.Nouns.Words[i] >= word
		})

		w.Nouns.Words = append(w.Nouns.Words, "")
		copy(w.Nouns.Words[index+1:], w.Nouns.Words[index:])
		w.Nouns.Words[index] = word
		w.Nouns.Count++
	} else if category == "adjectives" {
		index := sort.Search(w.Adjectives.Count, func(i int) bool {
			return w.Adjectives.Words[i] >= word
		})

		w.Adjectives.Words = append(w.Adjectives.Words, "")
		copy(w.Adjectives.Words[index+1:], w.Adjectives.Words[index:])
		w.Adjectives.Words[index] = word
		w.Adjectives.Count++
	} else {
		return fmt.Errorf("Category was neither noun or adjective, got: %s", category)
	}
	return nil
}

func (w *WordList) SearchWord(category, word string) int {
	var index int
	if category == "nouns" {
		index = sort.SearchStrings(w.Nouns.Words, word)
	} else if category == "adjectives" {
		index = sort.SearchStrings(w.Adjectives.Words, word)
	}
	return index
}

func (w *WordList) GetIndex(category string, index int) string {
	var word string
	if category == "nouns" {
		word = w.Nouns.Words[index]
	} else if category == "adjectives" {
		word = w.Adjectives.Words[index]
	}
	return word
}

func (w *WordList) getNoun() string {
	randomIndex := rand.Intn(w.Nouns.Count)
	randomString := w.Nouns.Words[randomIndex]
	return strings.ToLower(randomString)
}

func (w *WordList) getAdjective() string {
	randomIndex := rand.Intn(w.Adjectives.Count)
	randomString := w.Adjectives.Words[randomIndex]
	return strings.ToLower(randomString)
}

func LoadWords(filename string) (*WordList, error) {
	fileContent, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %w", err)
	}

	var wordList WordList

	err = json.Unmarshal(fileContent, &wordList)
	if err != nil {
		return nil, fmt.Errorf("error parsing JSON: %w", err)
	}

	return &wordList, nil
}
