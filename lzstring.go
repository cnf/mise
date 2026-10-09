package main

import (
	"fmt"
	"strings"
)

// LZString.decompressFromBase64, ported faithfully from the implementation that
// ships inside every InteractiveHtmlBom file. Only the decompress path is
// needed.
//
// Two details are load-bearing and easy to get wrong:
//   - the bit reader consumes the least-significant bit first and walks the
//     index stream, resetting the bit mask to 32 and pulling the next index at
//     each refill;
//   - base64 is LZString's own alphabet (A-Za-z0-9+/), mapped character by
//     character, NOT standard base64 — so it is not run through
//     encoding/base64, which rejects unpadded and index-64 ('=') input.
//
// If upstream ever changes the encoding, port the matching routine rather than
// patching this one.

const lzAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="

func decompressFromBase64(input string) (string, error) {
	if input == "" {
		return "", nil
	}
	index := make([]int, 0, len(input))
	for _, c := range input {
		v := strings.IndexRune(lzAlphabet, c)
		if v < 0 {
			return "", fmt.Errorf("invalid lzstring character %q", c)
		}
		index = append(index, v)
	}
	if len(index) == 0 {
		return "", nil
	}
	return lzDecompress(len(index), 32, func(i int) int {
		if i < 0 || i >= len(index) {
			return 0
		}
		return index[i]
	}), nil
}

func lzDecompress(length, resetValue int, getNext func(int) int) string {
	dictionary := map[int]string{}
	enlargeIn := 4
	dictSize := 4
	numBits := 3

	var result strings.Builder

	dataVal := getNext(0)
	dataPosition := resetValue
	dataIndex := 1

	readBits := func(n int) int {
		bits := 0
		maxpower := 1 << n
		power := 1
		for power != maxpower {
			resb := dataVal & dataPosition
			dataPosition >>= 1
			if dataPosition == 0 {
				dataPosition = resetValue
				dataVal = getNext(dataIndex)
				dataIndex++
			}
			if resb > 0 {
				bits |= power
			}
			power <<= 1
		}
		return bits
	}

	next := readBits(2)
	var c int
	switch next {
	case 0:
		c = readBits(8)
	case 1:
		c = readBits(16)
	case 2:
		return ""
	}

	w := string(rune(c))
	dictionary[3] = w
	result.WriteString(w)

	for {
		if dataIndex > length {
			return result.String()
		}
		cc := readBits(numBits)
		switch cc {
		case 0:
			dictionary[dictSize] = string(rune(readBits(8)))
			cc = dictSize
			dictSize++
			enlargeIn--
		case 1:
			dictionary[dictSize] = string(rune(readBits(16)))
			cc = dictSize
			dictSize++
			enlargeIn--
		case 2:
			return result.String()
		}
		if enlargeIn == 0 {
			enlargeIn = 1 << numBits
			numBits++
		}

		var entry string
		if s, ok := dictionary[cc]; ok {
			entry = s
		} else if cc == dictSize {
			entry = w + w[:1]
		} else {
			return result.String()
		}

		result.WriteString(entry)
		dictionary[dictSize] = w + entry[:1]
		dictSize++
		enlargeIn--
		w = entry

		if enlargeIn == 0 {
			enlargeIn = 1 << numBits
			numBits++
		}
	}
}
