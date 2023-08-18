package colsketch

import (
	"cmp"
	"sort"
)

// Code represents a dictionary code value.
type Code uint16

// IsExact returns true iff the code is an _exact_ code, i.e. a code which
// represents a single underlying value rather than a range of possible
// values. This is true iff the code is an even number.
func (c Code) IsExact() bool {
	return c%2 == 0
}

// Mode indicates whether to build a small Dict of up to 255 values or a larger one of up to 65535 values.
type Mode uint16

const (
	// Byte builds a Dict with up to 255 codes ranging over `[1,255]`. This mode is
	// most appropriate when building a sketch that elides accesses to smaller
	// underlying storage blocks like 64-byte cache lines, where the small
	// repertoire of codes (and thus higher per-element chance of a false
	// positive) is offset by the small size of each storage block (and thus
	// small number of elements).
	Byte Mode = iota

	// Word builds a Dict with up to 65535 codes ranging over `[1,65535]`. This
	// mode is most appropriate when building a sketch that elides access to
	// larger underlying storage blocks like 4096-byte pages, where the larger
	// number of elements per storage block demands a comparatively low false
	// positive probability per element.
	Word
)

// NumExactCodes returns the count of exact codes in the mode.
func (m Mode) NumExactCodes() int {
	switch m {
	case Byte:
		return 127
	case Word:
		return 32767
	default:
		return 0
	}
}

// MaxExactCode returns the maximum exact code in the mode.
func (m Mode) MaxExactCode() Code {
	switch m {
	case Byte:
		return 0xfe
	case Word:
		return 0xfffe
	default:
		return 0
	}
}

// MaxInexactCode returns the maximum inexact code in the mode.
func (m Mode) MaxInexactCode() Code {
	switch m {
	case Byte:
		return 0xff
	case Word:
		return 0xffff
	default:
		return 0
	}
}

// Dict is dictionary over an underlying type `T` conforming to cmp.Ordered. The
// dictionary maps underlying values to Codes to use in a sketch, using
// the Encode method.
type Dict[T cmp.Ordered] struct {
	// The mode the dictionary was built with.
	mode Mode

	// A sorted slice of the values assigned exact codes in the dictionary.
	// Implicitly defines both exact and inexact code values based on the
	// positions of exact codes in the slice.
	codes []T
}

// NewDict builds a dictionary with a given Mode over a provided sample.
func NewDict[T cmp.Ordered](mode Mode, sample []T) Dict[T] {
	if len(sample) == 0 {
		// For an empty sample we haven't much to work with; assign exact code 2
		// for the default value in the target type. Any value less than default
		// will code as 1, any value greater as 3. That's it.
		return Dict[T]{mode, make([]T, 1)}
	}

	// If we have a real sample, we want to sort it both to assign
	// order-preserving codes and to cluster it for frequency analysis.
	sortedSample := append([]T(nil), sample...)
	sort.Slice(sortedSample, func(i, j int) bool {
		return cmp.Less(sortedSample[i], sortedSample[j])
	})

	// Do the frequency analysis.
	clu := clusters(sortedSample)
	ncodes := mode.NumExactCodes()

	// If there are the same or fewer clusters than the codespace, we can
	// just assign one code per cluster, there's no need for anything
	// fancier.
	if len(clu) <= ncodes {
		codes := make([]T, len(clu))
		for i := range clu {
			codes[i] = clu[i].value
		}
		return Dict[T]{mode, codes}
	}

	codes := assignCodesWithMinimalStep(len(sample), ncodes, clu)
	return Dict[T]{mode, codes}
}

// Encode looks up the code for a value of the underlying value type `T`.
func (d *Dict[T]) Encode(value T) Code {
	idx := sort.Search(len(d.codes), func(i int) bool {
		return cmp.Compare(d.codes[i], value) >= 0
	})

	code := Code(2 * (idx + 1))
	if idx >= len(d.codes) || cmp.Compare(d.codes[idx], value) != 0 {
		code--
	}
	return code
}

// Len returns the number of codes in the dictionary.
func (d *Dict[T]) Len() int {
	return len(d.codes)
}

// cluster holds information about a cluster of identical values in
// a sample.
type cluster[T cmp.Ordered] struct {
	value T
	count int
}

// clusters performs frequency analysis on a sorted sample.
func clusters[T cmp.Ordered](sortedSample []T) []cluster[T] {
	if len(sortedSample) == 0 {
		return nil
	}

	clu := make([]cluster[T], 0, len(sortedSample))
	curr, count := sortedSample[0], 0

	for _, s := range sortedSample {
		if cmp.Compare(s, curr) == 0 {
			count++
			continue
		}

		clu = append(clu, cluster[T]{curr, count})
		curr, count = s, 1
	}

	return append(clu, cluster[T]{curr, count})
}

// assignCodesWithMinimalStep divides a list of clusters into segments and assigns a code to represent each segment.
// The function aims to distribute the clusters across a specified number of codes (ncodes) such that each code
// represents roughly the same number of sample values.
// The initial estimation for how many sample values each code should cover might be off due to varying cluster sizes.
// To correct any inaccuracies, the function iteratively refines the estimation using a bias correction mechanism,
// ensuring that the resulting number of codes is as close as possible to ncodes without exceeding it.
func assignCodesWithMinimalStep[T cmp.Ordered](sampleSize, ncodes int, clu []cluster[T]) []T {
	// Each code should cover at least codestep worth of the sample.
	codestep := sampleSize / ncodes

	// We start with a basic dictionary with each code covering `codestep`
	// sample vaules, calculated by taking elements from the cluster list.
	codes := assignCodesWithStep(codestep, clu)

	// Unfortunately it's possible some of those clusters overshoot the
	// `codestep`, giving us codes that cover too many sample values and
	// therefore giving us too few overall codes. To correct for this, we
	// want to iterate a few times (up to 8 times -- ad-hoc limit)
	// estimating the error, reducing the `codestep` and re-encoding, to try
	// to get as close as possible (without going over) the target number of
	// codes.
	for i := 0; i < 8; i++ {
		if len(codes) == ncodes {
			break
		}

		if len(codes) > ncodes {
			codes = codes[:ncodes]
			break
		}

		// Calculate the bias as the ratio of the actual number of codes to the desired number.
		// We multiply by 10000 to avoid floating-point arithmetic and maintain precision using integers.
		bias := (len(codes) * 10000) / ncodes

		// Adjust the codestep based on the calculated bias.
		// Dividing by 10000 brings the value back to its original scale.
		codestep = (codestep * bias) / 10000

		// Attempt to assign codes again with the adjusted codestep
		next := assignCodesWithStep(codestep, clu)
		if len(next) < ncodes {
			codes = next
		} else {
			break
		}
	}

	return codes
}

// assignCodesWithStep selects representative codes from a list of clusters based on a given step size (codestep).
// Each code represents a sequence of clusters such that the sum of their counts is approximately codestep.
// The representative code for a sequence is chosen as the value of the cluster with the maximum count within that sequence.
func assignCodesWithStep[T cmp.Ordered](codestep int, clu []cluster[T]) []T {
	// Initialize an empty list of codes.
	var codes []T
	firstIdx := 0

	// Iterate over the clusters to assign codes.
	for firstIdx < len(clu) {
		// Initialize indices and counters for this sequence of clusters.
		lastIdx, idxWithMaxVal, clusterCountSum := firstIdx, firstIdx, 0

		// Sum the counts of clusters in the sequence until the sum reaches or exceeds codestep.
		for lastIdx < len(clu) && clusterCountSum < codestep {
			// Update idxWithMaxVal if the current cluster has a count greater than the previously observed max.
			if clu[idxWithMaxVal].count < clu[lastIdx].count {
				idxWithMaxVal = lastIdx
			}
			clusterCountSum += clu[lastIdx].count
			lastIdx++
		}

		// Add the value of the cluster with the maximum count in this sequence to the list of codes.
		codes = append(codes, clu[idxWithMaxVal].value)

		// Move to the next cluster for the subsequent sequence.
		firstIdx = lastIdx + 1
	}

	return codes
}
