package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

const wordSimilarityThreshold = 0.6

type posTrgm struct {
	trg   string
	index int
}

// https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L628
func calcWordSimilarity(s1, s2 string) float64 {
	trg1 := generateTrgmOnly(s1)
	trg2 := generateTrgmOnly(s2)

	ptrg := makePositionalTrgm(trg1, trg2)
	slices.SortStableFunc(ptrg, compPtrgm)

	trg2indexes := make([]int, len(trg2))
	found := make([]bool, len(trg1))

	ulen1 := 0
	j := 0

	for i := 0; i < len(trg1); i++ {
		if i > 0 {
			r := cmp.Compare(ptrg[i-1].trg, ptrg[i].trg)
			if r != 0 {
				if found[j] {
					ulen1++
				}
				j++
			}
		}

		if ptrg[i].index >= 0 {
			trg2indexes[ptrg[i].index] = j
		} else {
			found[j] = true
		}
	}
	if found[j] {
		ulen1++
	}

	res := iterateWordSimilarity(trg2indexes, found, ulen1, len(trg1), len(trg2))

	return res
}

// https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L277
func generateTrgmOnly(s string) []string {
	if len(s) == 0 {
		return []string{}
	}

	s = strings.ToLower(s)
	sSplit := strings.Split(s, " ")
	for i := range sSplit {
		sSplit[i] = "  " + sSplit[i] + "  " // add two padding spaces as per https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L318
	}

	var res []string
	for i := range sSplit {
		res = append(res, makeTrigrams(sSplit[i])...)
	}

	return res
}

// https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L225
func makeTrigrams(s string) []string {
	if len(s) < 3 {
		return []string{}
	}

	var res []string
	for i := 0; i <= len(s)-3; i++ {
		res = append(res, s[i:i+3])
	}

	return res
}

// https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L399
func makePositionalTrgm(trgm1, trgm2 []string) []posTrgm {
	res := make([]posTrgm, len(trgm1)+len(trgm2))

	for i := range trgm1 {
		res[i] = posTrgm{trg: trgm1[i], index: -1}
	}

	for i := range trgm2 {
		res[len(trgm1)+i] = posTrgm{trgm2[i], i}
	}

	return res
}

// https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L426
func compPtrgm(trgm1, trgm2 posTrgm) int {
	r := cmp.Compare(trgm1.trg, trgm2.trg)
	if r != 0 {
		return r
	}

	return cmp.Compare(trgm1.index, trgm2.index)
}

// https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L460
func iterateWordSimilarity(trg2indexes []int, found []bool, ulen1, len1, len2 int) float64 {
	var (
		ulen2   = 0
		count   = 0
		upper   = -1
		smlrMax = 0.0
	)

	threshold := wordSimilarityThreshold
	lower := -1
	lastPos := make([]int, len1)
	for i := range lastPos {
		lastPos[i] = -1
	}

	for i := 0; i < len2; i++ {
		trgIndex := trg2indexes[i]

		if lower >= 0 || found[trgIndex] {
			if lastPos[trgIndex] < 0 {
				ulen2++
				if found[trgIndex] {
					count++
				}
			}
			lastPos[trgIndex] = i
		}

		if found[trgIndex] { // assume we have no flags so only check if found[trgIndex]: https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L522
			upper = i
			if lower == -1 {
				lower = i
				ulen2 = 1
			}

			smlrCur := calcSml(count, ulen1, ulen2)

			tmpCount := count
			tmpUlen2 := ulen2
			prevLower := lower

			for tmpLower := lower; tmpLower < upper; tmpLower++ {
				smlrTmp := calcSml(tmpCount, ulen1, tmpUlen2)
				if smlrTmp > smlrCur {
					smlrCur = smlrTmp
					ulen2 = tmpUlen2
					lower = tmpLower
					count = tmpCount
				}
				if smlrCur >= threshold { // not sure about WORD_SIMILARITY_CHECK_ONLY flag here, assume it's TRUE: https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L570
					break
				}

				tmpTrgindex := trg2indexes[tmpLower]
				if lastPos[tmpTrgindex] == tmpLower {
					tmpUlen2--
					if found[tmpTrgindex] {
						tmpCount--

					}
				}
			}

			smlrMax = max(smlrMax, smlrCur)

			if smlrMax > threshold { // not sure about WORD_SIMILARITY_CHECK_ONLY flag here, assume it's TRUE: https://github.com/postgres/postgres/blob/004dbbd72f7505105a10d4e8ccb9a5a5d87125ed/contrib/pg_trgm/trgm_op.c#L590
				break
			}

			for tmpLower := prevLower; tmpLower < lower; tmpLower++ {
				tmpTrgindex := trg2indexes[tmpLower]
				if lastPos[tmpTrgindex] == tmpLower {
					lastPos[tmpTrgindex] = -1
				}
			}
		}
	}

	return smlrMax
}

// https://github.com/postgres/postgres/blob/c7fc8808a91ed1b5810abb5f6043be7b6d58dbcf/contrib/pg_trgm/trgm.h#L108
func calcSml(count, len1, len2 int) float64 {
	return float64(count) / float64(len1+len2-count)
}

func main() {
	res := calcWordSimilarity("banana", "bananas")
	fmt.Printf("result is %f\n", res) // expected result is 0.833333
}
