package main

// A test case, with With punctuation. zxyz. Make sure the oddball on this line scores highly. (It does.)
// Also make sure that "with" shows up doubled despite the case change.

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const knownWordsFile = "/usr/local/plan9/lib/words"

type Word struct {
	word    string
	file    string
	lineNum int
	byteNum int
	single  float64
	double  float64
}

func (w Word) String() string {
	return fmt.Sprintf("%s:%d:%d %s", w.file, w.lineNum, w.byteNum, w.word)
}

func (w Word) print() bool {
	return false
}

// Sort interface for []*Word
type Typo []*Word

func (t Typo) Len() int {
	return len(t)
}

func (t Typo) Less(i, j int) bool {
	w1 := t[i]
	w2 := t[j]
	return w1.single*w1.double > w2.single*w2.double // sort down
}

func (t Typo) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

var words = make([]Word, 0, 1000)
var known = make(map[string]bool) // loaded from knownWordsFile

var (
	singleCount int
	single      = make(map[rune]int)
	doubleCount int
	double      = make(map[rune]map[rune]int)
)

func main() {
	flag.Parse()
	for _, w := range readLines(knownWordsFile, nil) {
		known[w] = true
	}
	if len(flag.Args()) == 0 {
		add("<stdin>", os.Stdin)
	} else {
		for _, f := range flag.Args() {
			add(f, nil)
		}
	}
	doubles()
	stats()
	spell()
}

func readLines(file string, fd *os.File) []string {
	if fd == nil {
		var err error
		fd, err = os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stdout, "typo: %s\n", file, err)
			os.Exit(2)
		}
	}
	bytes, err := ioutil.ReadAll(fd)
	if err != nil {
		fmt.Fprintf(os.Stdout, "typo: reading %s: %s\n", file, err)
		os.Exit(2)
	}
	return strings.Split(string(bytes), "\n")
}

func add(file string, fd *os.File) {
	lines := readLines(file, fd)
	for lineNum, line := range lines {
		inWord := false
		wordStart := 1
		for byteNum, c := range line {
			switch {
			case inWord && unicode.IsSpace(c):
				addWord(line[wordStart:byteNum], file, lineNum+1, wordStart+1)
				inWord = false
			case !inWord && !unicode.IsSpace(c):
				inWord = true
				wordStart = byteNum
			}
		}
		if inWord {
			addWord(line[wordStart:], file, lineNum+1, wordStart+1)
		}
	}
}

func addWord(word, file string, lineNum, byteNum int) {
	word = strings.TrimFunc(word, unicode.IsPunct)
	// There must be a letter.
	hasLetter := false
	for _, c := range word {
		if unicode.IsLetter(c) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return
	}
	words = append(words, Word{word: word, file: file, lineNum: lineNum, byteNum: byteNum})
}

func onlyLower(s string) bool {
	for _, c := range s {
		if !unicode.IsLower(c) {
			return false
		}
	}
	return true
}

func lower(s string) string {
	// Avoid allocation if already lower.
	if onlyLower(s) {
		return s
	}
	return strings.ToLower(s)
}

func doubles() {
	prev := ""
	for i := range words {
		w := lower(words[i].word)
		if w == prev {
			fmt.Printf("%s repeats\n", words[i])
		}
		prev = w
	}
}

func stats() {
	// Count singles.
	for _, w := range words {
		for _, r := range w.word {
			single[r]++
			singleCount++
		}
	}
	maxSingle := 0
	for _, count := range single {
		if count > maxSingle {
			maxSingle = count
		}
	}
	// Scores are 1.0 for a word that has only the most common letter/sequence.
	// We add the cubes of the rarity of each letter or letter pair, so really unlikely
	// things skew highly.
	for i := range words {
		unlikelihood := 0.0
		w := &words[i]
		for _, c := range w.word {
			count := single[c]
			ratio := float64(maxSingle) / float64(count)
			unlikelihood += ratio * ratio * ratio
		}
		w.single = unlikelihood / float64(maxSingle*utf8.RuneCountInString(w.word))
	}
	// Count doubles.
	for _, w := range words {
		var prev rune
		for i, r := range w.word {
			if i > 0 {
				m := double[prev]
				if m == nil {
					m = make(map[rune]int)
					double[prev] = m
				}
				m[r]++
				doubleCount++
			}
			prev = r
		}
	}
	maxDouble := 0
	for _, m := range double {
		for _, count := range m {
			if count > maxDouble {
				maxDouble = count
			}
		}
	}
	for i := range words {
		w := &words[i]
		if len(w.word) < 2 {
			continue
		}
		unlikelihood := 0.
		var prev rune
		for i, r := range w.word {
			if i > 0 {
				count := double[prev][r]
				ratio := float64(maxDouble) / float64(count)
				unlikelihood += ratio * ratio * ratio
			}
			prev = r
		}
		w.double = unlikelihood / float64(maxDouble*(utf8.RuneCountInString(w.word)-1))
	}
}

func spell() {
	// Thresholds are empirically and somewhat arbitrarily determined.
	const threshold = 1.0
	const productThreshold = 1e7
	typos := make(Typo, 0, 100)
	for i := range words {
		w := &words[i]
		if known[w.word] {
			continue
		}
		wLower := lower(w.word)
		// If we know the word or it has no double score, skip it.
		if known[wLower] {
			continue
		}
		if w.double == 0 {
			continue
		}
		// If the unlikeliness score is low, skip it.
		if w.single < threshold && w.double < threshold {
			continue
		}
		// Boost its unlikelihood if it's only lower-case letters, possibly after an initial cap,
		// since those are even likelier to be typos. We want them to show up high in the list.
		afterFirst := func(s string) string {
			_, w := utf8.DecodeRuneInString(s)
			return s[w:]
		}
		if onlyLower(w.word) || onlyLower(afterFirst(w.word)) {
			w.single *= w.single
			w.double *= w.double
		}
		if w.single*w.double < productThreshold {
			continue
		}
		typos = append(typos, w)
	}
	sort.Sort(typos)
	for _, w := range typos {
		fmt.Printf("%s %d\n", w, int(math.Log(w.single*w.double)-10))
	}
}
