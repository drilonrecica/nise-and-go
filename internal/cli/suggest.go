package cli

// suggest returns the candidate closest to input by Levenshtein edit
// distance, and whether it is close enough to be worth showing as a
// "did you mean" hint. Ties keep the first match in candidates (registry
// declaration order), which keeps the suggestion deterministic.
//
// The threshold — at most half of input's length, rounded up, with a
// minimum of 1 and a cap of 3 — is a plain heuristic, not a standard: it is
// meant to catch a small slip (a swapped, missing, or extra letter) without
// suggesting an unrelated command for wildly different input.
func suggest(input string, candidates []string) (string, bool) {
	if input == "" || len(candidates) == 0 {
		return "", false
	}

	best := ""
	bestDist := -1
	for _, c := range candidates {
		d := levenshtein(input, c)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = c
		}
	}

	threshold := (len(input) + 1) / 2
	if threshold < 1 {
		threshold = 1
	}
	if threshold > 3 {
		threshold = 3
	}
	if bestDist > threshold {
		return "", false
	}
	return best, true
}

// levenshtein returns the edit distance between a and b: the minimum
// number of single-character insertions, deletions, or substitutions to
// turn a into b. Standard dynamic-programming implementation, O(len(a) *
// len(b)) time and O(min(len(a), len(b))) space.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) > len(br) {
		ar, br = br, ar
	}
	prev := make([]int, len(ar)+1)
	for i := range prev {
		prev[i] = i
	}
	curr := make([]int, len(ar)+1)
	for j := 1; j <= len(br); j++ {
		curr[0] = j
		for i := 1; i <= len(ar); i++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[i] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(ar)]
}
