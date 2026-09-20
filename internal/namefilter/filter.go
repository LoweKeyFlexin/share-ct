// Package namefilter implements the versioned, portable username digest policy.
// It checks names only at identity writes and public display reads, never at score
// submission or reaction input. The bundled digests obscure the reviewed terms
// from casual source inspection; hashes of short words are not secrets.
package namefilter

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	Version       = 1
	Normalization = "ASCII_UPPER_SPACE_V1"
	header        = "CT-NAME-FILTER\t1\tASCII_UPPER_SPACE_V1\tSHA-256"
	domain        = "CT-NAME-FILTER-v1\x00"
)

//go:embed blocked-names-v1.sha256
var asset string

type digestKey struct {
	length int
	hash   [sha256.Size]byte
}

type policy struct {
	whole      map[digestKey]struct{}
	token      map[digestKey]struct{}
	substring  map[digestKey]struct{}
	subLengths []int
}

var active = mustParse(asset)

func mustParse(data string) policy {
	p, err := parse(data)
	if err != nil {
		panic("invalid embedded username policy: " + err.Error())
	}
	return p
}

func parse(data string) (policy, error) {
	p := policy{whole: make(map[digestKey]struct{}), token: make(map[digestKey]struct{}), substring: make(map[digestKey]struct{})}
	scanner := bufio.NewScanner(strings.NewReader(data))
	if !scanner.Scan() || scanner.Text() != header {
		return p, fmt.Errorf("bad policy header")
	}
	for line := 2; scanner.Scan(); line++ {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) != 3 {
			return p, fmt.Errorf("line %d: expected mode, length and hash", line)
		}
		length, err := strconv.Atoi(parts[1])
		if err != nil || length < 1 || length > 256 {
			return p, fmt.Errorf("line %d: invalid byte length", line)
		}
		if len(parts[2]) != sha256.Size*2 || strings.ToLower(parts[2]) != parts[2] {
			return p, fmt.Errorf("line %d: invalid digest encoding", line)
		}
		bytes, err := hex.DecodeString(parts[2])
		if err != nil {
			return p, fmt.Errorf("line %d: invalid digest: %w", line, err)
		}
		var hash [sha256.Size]byte
		copy(hash[:], bytes)
		key := digestKey{length: length, hash: hash}
		var bucket map[digestKey]struct{}
		switch parts[0] {
		case "whole":
			bucket = p.whole
		case "token":
			bucket = p.token
		case "substring":
			bucket = p.substring
			p.subLengths = append(p.subLengths, length)
		default:
			return p, fmt.Errorf("line %d: invalid mode", line)
		}
		if _, exists := bucket[key]; exists {
			return p, fmt.Errorf("line %d: duplicate digest", line)
		}
		bucket[key] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return p, err
	}
	if len(p.whole)+len(p.token)+len(p.substring) == 0 {
		return p, fmt.Errorf("empty policy")
	}
	sort.Ints(p.subLengths)
	lengths := p.subLengths[:0]
	for _, n := range p.subLengths {
		if len(lengths) == 0 || lengths[len(lengths)-1] != n {
			lengths = append(lengths, n)
		}
	}
	p.subLengths = lengths
	return p, nil
}

// AssetSHA256 identifies exactly which digest revision is in this server binary.
func AssetSHA256() string {
	sum := sha256.Sum256([]byte(asset))
	return hex.EncodeToString(sum[:])
}

// Rejects applies the reviewed ASCII policy to an otherwise valid username.
// Unicode names remain valid under the existing character rule; a separately
// reviewed, versioned Unicode policy is required before claiming coverage there.
func Rejects(raw string) bool { return active.rejects(raw) }

func (p policy) rejects(raw string) bool {
	normalized, ok := normalizeASCII(raw)
	if !ok {
		return false
	}
	if p.rejectsCandidate(normalized) {
		return true
	}
	compact := strings.ReplaceAll(normalized, " ", "")
	if compact != normalized && p.rejectsCandidate(compact) {
		return true
	}
	folded := foldOtherDigits(normalized)
	if folded != normalized && p.rejectsCandidate(folded) {
		return true
	}
	if compact != normalized {
		foldedCompact := foldOtherDigits(compact)
		if foldedCompact != compact && p.rejectsCandidate(foldedCompact) {
			return true
		}
	}
	return false
}

func normalizeASCII(raw string) (string, bool) {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == ' ':
			if len(out) > 0 && out[len(out)-1] != ' ' {
				out = append(out, ' ')
			}
		case c >= 'a' && c <= 'z':
			out = append(out, c-'a'+'A')
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			return "", false
		}
	}
	if len(out) > 0 && out[len(out)-1] == ' ' {
		out = out[:len(out)-1]
	}
	return string(out), len(out) > 0
}

// The reviewed policy asset includes every bounded I/L-to-1 variant. Leaving
// literal 1 intact at runtime lets one name mix the two substitutions without
// a combinatorial number of hashes per request.
func foldOtherDigits(input string) string {
	out := []byte(input)
	for i, c := range out {
		switch c {
		case '0':
			out[i] = 'O'
		case '3':
			out[i] = 'E'
		case '4':
			out[i] = 'A'
		case '5':
			out[i] = 'S'
		case '7':
			out[i] = 'T'
		case '8':
			out[i] = 'B'
		}
	}
	return string(out)
}

func digest(mode, value string) digestKey {
	input := make([]byte, 0, len(domain)+len(mode)+1+len(value))
	input = append(input, domain...)
	input = append(input, mode...)
	input = append(input, 0)
	input = append(input, value...)
	return digestKey{length: len(value), hash: sha256.Sum256(input)}
}

func has(bucket map[digestKey]struct{}, mode, value string) bool {
	_, ok := bucket[digest(mode, value)]
	return ok
}

func (p policy) rejectsCandidate(candidate string) bool {
	if has(p.whole, "whole", candidate) {
		return true
	}
	for _, token := range strings.Split(candidate, " ") {
		if has(p.token, "token", token) {
			return true
		}
		for _, length := range p.subLengths {
			if length > len(token) {
				break
			}
			for start := 0; start+length <= len(token); start++ {
				if has(p.substring, "substring", token[start:start+length]) {
					return true
				}
			}
		}
	}
	return false
}
