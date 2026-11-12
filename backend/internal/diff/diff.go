package diff

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Line struct {
	Path   string
	Number int
	Text   string
	Added  bool
	Hunk   int
	Index  int
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func Parse(diff string) ([]Line, error) {
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	var result []Line
	var path string
	var lineNumber int
	var hunk int
	var inHunk bool
	index := -1
	for scanner.Scan() {
		index++
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			path, inHunk = "", false
		case !inHunk && strings.HasPrefix(line, "+++ b/"):
			path = strings.TrimPrefix(line, "+++ b/")
		case !inHunk && strings.HasPrefix(line, "+++ /dev/null"):
			path, inHunk = "", false
		case strings.HasPrefix(line, "@@ "):
			match := hunkHeader.FindStringSubmatch(line)
			if match == nil {
				return nil, fmt.Errorf("invalid diff hunk")
			}
			lineNumber, _ = strconv.Atoi(match[1])
			hunk++
			inHunk = true
		case inHunk && path != "" && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, " ")):
			result = append(result, Line{Path: path, Number: lineNumber, Text: line[1:], Added: line[0] == '+', Hunk: hunk, Index: index})
			lineNumber++
		}
	}
	return result, scanner.Err()
}
