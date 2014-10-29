// Copyright 2013 The rspace Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Typo is a modern version of the original Unix typo command, a scan of whose man page
// is at
//   http://cmd.rspace.googlecode.com/hg/typo/typo.png
// This version ignores nroff but handles Unicode and can strip simple HTML tags.
// It provides location information for each typo, including the byte number on the line.
// It also identifies repeated words, a a typographical error that occurs often.
//
// The -r flag suppresses reporting repeated words.
// The -n and -t flags control how many "typos" to print.'
// The -html flag enables simple filtering of HTML from the input.
//
// See the comments in the source for a description of the algorithm, extracted
// from Bell Labs CSTR 18 by Robert Morris and Lorinda L. Cherry.
//
package main // import "robpike.io/cmd/typo"

// A test case, with With punctuation. zxyz. Make sure the oddball on this line scores highly. (It does.)
// Also make sure that "with" shows up doubled despite the case change.

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

var knownWordsFiles = []string{"words", "w2006.txt"}
var (
	nTypos     = flag.Int("n", 50, "maximum number of words to print")
	noRepeats  = flag.Bool("r", false, "don't show repeated words words")
	threshold  = flag.Int("t", 10, "cutoff threshold; smaller means more words")
	filterHTML = flag.Bool("html", false, "filter HTML tags from input")
)

var (
	goroot string
	gopath string
)

func init() {
	gopath = os.Getenv("GOPATH")
	if unicode.IsPunct('<') {
		// This must be true for HTML filtering to work.
		fmt.Fprintf(os.Stderr, "typo: unicode says < is punctuation")
		*filterHTML = false
	}
}

func main() {
	flag.Parse()
	for _, f := range knownWordsFiles {
		path, ok := getPath(f)
		if !ok {
			fmt.Fprintf(os.Stderr, "typo: can't find known words file %q\n", f)
			continue
		}
		for _, w := range read(path, nil, bufio.ScanWords) {
			known[w] = true
		}
	}
	if len(flag.Args()) == 0 {
		add("<stdin>", os.Stdin)
	} else {
		for _, f := range flag.Args() {
			add(f, nil)
		}
	}
	repeats()
	stats()
	spell()
}

func getPath(base string) (string, bool) {
	if gopath != "" {
		d := filepath.Join(gopath, "src", "robpike.io", "cmd", "typo")
		path := filepath.Join(d, base)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	path := filepath.Join(string(os.PathSeparator), "usr", "local", "plan9", "lib", base)
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return "", false
}

type Word struct {
	text    string  // The original.
	lower   *string // The word in lower case; may point to original.
	file    string
	lineNum int
	byteNum int
	score   int
}

func (w Word) String() string {
	if w.score == 0 {
		return fmt.Sprintf("%s:%d:%d %s", w.file, w.lineNum, w.byteNum, w.text)
	} else {
		return fmt.Sprintf("%s:%d:%d [%d] %s", w.file, w.lineNum, w.byteNum, w.score, w.text)
	}
}

// Sort interfaces for []*Word
type ByScore []*Word

func (t ByScore) Len() int {
	return len(t)
}

func (t ByScore) Less(i, j int) bool {
	w1 := t[i]
	w2 := t[j]
	return w1.score > w2.score // Sort down
}

func (t ByScore) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

type ByWord []*Word

func (t ByWord) Len() int {
	return len(t)
}

func (t ByWord) Less(i, j int) bool {
	w1 := t[i]
	w2 := t[j]
	return w1.text < w2.text
}

func (t ByWord) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

var words = make([]*Word, 0, 1000)
var known = make(map[string]bool) // loaded from knownWordsFiles

func read(file string, fd *os.File, split bufio.SplitFunc) []string {
	if fd == nil {
		var err error
		fd, err = os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stdout, "typo: %s\n", err)
			os.Exit(2)
		}
		defer fd.Close()
	}
	scanner := bufio.NewScanner(fd)
	scanner.Split(split)
	words := make([]string, 0, 1000)
	for scanner.Scan() {
		words = append(words, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stdout, "typo: reading %s: %s\n", file, err)
		os.Exit(2)
	}
	return words
}

func add(file string, fd *os.File) {
	lines := read(file, fd, bufio.ScanLines)
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

// leadingHTMLLen returns the length of all HTML tags at the start of the text.
// It assumes a tag goes from an initial "<" to the first closing ">".
func leadingHTMLLen(text string) int {
	if len(text) == 0 || text[0] != '<' {
		return 0
	}
	n := strings.IndexRune(text, '>')
	if n < 0 {
		return 0
	}
	n++ // Absorb closing '>'.
	return n + leadingHTMLLen(text[n:])
}

// trailingHTMLLen mirrors leadingHTMLLen at the end of the text.
func trailingHTMLLen(text string) int {
	if len(text) == 0 || text[len(text)-1] != '>' {
		return 0
	}
	// TODO: There should be a LastIndexRune.
	n := strings.LastIndex(text, "<")
	if n < 0 {
		return 0
	}
	return len(text) - n + trailingHTMLLen(text[:len(text)-n])
}

func addWord(text, file string, lineNum, byteNum int) {
	// Note: '<' is not punctuation according to Unicode.
	n := len(text)
	text = strings.TrimLeftFunc(text, unicode.IsPunct)
	byteNum += n - len(text)
	text = strings.TrimRightFunc(text, unicode.IsPunct)
	if *filterHTML {
		// Easily defeated by spaces and newlines, but gets things like <code><em>foo</em></code>.
		n := leadingHTMLLen(text)
		text = text[n:]
		byteNum += n
		n = trailingHTMLLen(text)
		text = text[:len(text)-n]
		if len(text) == 0 {
			return
		}
		n = len(text)
		text = strings.TrimLeftFunc(text, unicode.IsPunct)
		byteNum += n - len(text)
		text = strings.TrimRightFunc(text, unicode.IsPunct)
	}
	// There must be a letter.
	hasLetter := false
	for _, c := range text {
		if unicode.IsLetter(c) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return
	}
	word := &Word{
		text:    text,
		file:    file,
		lineNum: lineNum,
		byteNum: byteNum,
	}
	if onlyLower(text) {
		word.lower = &word.text
	} else {
		x := strings.ToLower(text)
		word.lower = &x
	}
	words = append(words, word)
}

func onlyLower(s string) bool {
	for _, c := range s {
		if !unicode.IsLower(c) {
			return false
		}
	}
	return true
}

func repeats() {
	if *noRepeats {
		return
	}
	prev := ""
	for _, word := range words {
		w := *word.lower
		if w == prev {
			fmt.Printf("%s repeats\n", word)
		}
		prev = w
	}
}

type digram [2]rune
type trigram [3]rune

var diCounts = make(map[digram]int)
var triCounts = make(map[trigram]int)

func stats() {
	// Compute global digram and trigram counts.
	for _, word := range words {
		addDigrams(word.text)
		scanTrigrams(word.text, incTrigrams)
	}
	// Compute the score for the word.
	for _, word := range words {
		if known[*word.lower] {
			continue
		}
		word.score = int(score(word.text))
	}
}

func scanTrigrams(word string, fn func(t trigram)) {
	// For "once", we have ".on", "onc", "nce", "ce."
	// Do the first one by hand to prime the pump.
	rune, wid := utf8.DecodeRuneInString(word)
	t := trigram{'.', '.', rune}
	for _, r := range word[wid:] {
		t[0] = t[1]
		t[1] = t[2]
		t[2] = r
		fn(t)
	}
	// At this point, we have "nce"; make "ce.".
	// If there was only one letter, "a", we have "..a" and this tail will give us ".a.", which is what we want.
	// Final marker
	t[0] = t[1]
	t[1] = t[2]
	t[2] = '.'
	fn(t)
}

func addDigrams(word string) {
	d := digram{'.', '.'}
	// For "once", we have ".o", "on", "nc", "ce", "e."
	for _, r := range word {
		d[0] = d[1]
		d[1] = r
		diCounts[d]++
	}
	// Final marker
	d[0] = d[1]
	d[1] = '.'
	diCounts[d]++
}

func incTrigrams(t trigram) {
	triCounts[t]++
}

func triScore(t trigram) float64 {
	nxy := float64(diCounts[digram{t[0], t[1]}] - 1)
	nyz := float64(diCounts[digram{t[1], t[2]}] - 1)
	nxyz := float64(triCounts[t] - 1)
	// The paper says to use -10 for log(0), but its square is 100, so that can't be right.
	if nxy == 0 || nyz == 0 || nxyz == 0 {
		return 0
	}
	logNxy := math.Log(nxy)
	logNyz := math.Log(nyz)
	logNxyz := math.Log(nxyz)
	return 0.5*(logNxy+logNyz) - logNxyz
}

func score(word string) float64 {
	sumOfSquares := 0.0
	n := 0
	fn := func(t trigram) {
		i := triScore(t)
		sumOfSquares += i * i
		n++
	}
	scanTrigrams(word, fn)
	return 10 / math.Sqrt(sumOfSquares/float64(n))
}

func spell() {
	// Uniq the list: show each word only once; also drop known words.
	sort.Sort(ByWord(words))
	out := words[0:0]
	prev := " "
	for _, word := range words {
		if word.text == prev {
			continue
		}
		if known[*word.lower] {
			continue
		}
		out = append(out, word)
		prev = word.text
	}
	words = out
	// Sort the words by unlikelihood and print the top few.
	sort.Sort(ByScore(words))
	for i, w := range words {
		if i >= *nTypos {
			break
		}
		if w.score < *threshold {
			break
		}
		fmt.Println(words[i])
	}
}

/*
Thanks to Doug McIlroy for digging this out for me:

From CSTR 18, Robert Morris and Lorinda L. Cherry, "Computer
detection of  typographical errors", July 1974.

... a count is kept of each kind of digram and trigram in the
document. For example, if the word `once' appears in the text,
the counts corresponding to each of these five digrams and four
trigrams are incremented.

 .o on nc ce e.
 .on onc nce ce.

where the `.' signifies the beginning or ending of a word. These
statistics are retained for later processing.

... 2726 common technical English words as used at Murray Hill
... are removed. [The list was created] by processing about one
million words of technical text appearing in documents produced on
the UNIX time-sharing system at Murray Hill. The words selected were
those which have the highest probability of appearing in a given
document. This is not the same as a list of the most frequently
occurring words and, in fact, some very rare words occur in the
table. For example, `murray' and `abstract' are very uncommon words
in the documents sampled, yet they both appear once in virtually
every document.

... a number is attached to each word by consulting the table of
digrams and trigrams previously produced.

The number is an index of peculiarity and reflects the likelihood
of the hypothesis that the trigrams in the given word were produced
from the same source that produced the trigram table.

Each trigram in the current word is treated separately. An initial
and terminal trigram is treated for each word so that the number
of trigrams in a word becomes equal to the number of characters
in the word. For each trigram T = (xyz) in the current word,
the digram and trigram counts from the prepared table are accessed.
Each such count is reduced by one to remove the effect of the
current word on the statistics. The resulting counts are n(xy),
n(yz), and n(xyz). The index i(T) for the trigram T is set  equal to

     i(T) = (1/2)[log n(xy) + log n(yz)] - log n(xyz)

This index is invariant with the size of the document, i.e. it needs
no normalization.  It measures the evidence against the hypothesis
that the trigram was produced by the same source that produced the
digram and trigram tables in the sense of References 1 and 2. In the
case that one of the digram or trigram counts is zero, the log of
that count is taken to be -10.

A variety of methods were tried to combine the trigram indices to
obtain an index for the whole word, including

- the average of trigram indices,
- the square root of the average of the squares of the trigram
  indices, and
- the largest of the trigram indices.

All were moderately effective; the first tended to swamp out the
effect of a really strange trigram in a long word and the latter
was insensitive to a sequence of strange trigrams whose cumulative
evidence should have made a word suspcious. The second on trial
appeared to be a satisfactory compromise and was adopted.

The words with their attached indices are sorted on the index in
descending order and printed in a pleasing three-column format. [The
printed integer index values are] normalized in such a way that
a word with an index greater than 10 contains trigrams that are not
representative of the remainder of the document. Such a word almost
certainly appears just once in a document.

...


[1] Good, I. j. Probability and the Weighing of Evidence. Charles
Griffin & Co., London, 1950.

[2] Kullback, S. and Leibler, R. A. On Information and Sufficiency.
Annals of Math. Stat., 22 (1951), pp. 79-86
*/
