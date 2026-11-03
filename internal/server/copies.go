package server

import (
	"bytes"
	"strconv"
)

const (
	copiesMin = 1
	copiesMax = 10
)

// copies is how many labels a TSPL job prints: the sum over its PRINT m[,n]
// commands of m sets times n copies of each. It is -1 when a PRINT command
// does not parse, so that the job is refused.
//
// The agent knows no more of TSPL than this, so a new label layout does not
// need a new agent.
func copies(job []byte) int {
	total := 0
	for line := range bytes.Lines(job) {
		f := bytes.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' || r == '\r' || r == '\n' })
		if len(f) == 0 || !bytes.EqualFold(f[0], []byte("PRINT")) {
			continue
		}
		if len(f) < 2 || len(f) > 3 {
			return -1
		}
		n := 1
		for _, arg := range f[1:] {
			v, err := strconv.Atoi(string(arg))
			if err != nil || v < 0 || v > copiesMax {
				return -1
			}
			n *= v
		}
		total += n
		if total > copiesMax {
			return total
		}
	}
	return total
}
