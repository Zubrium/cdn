// Package engine_data validates bounded JSON documents accepted by engine records.
package engine_data

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	domainengine "github.com/Zubrium/cdn/core/domain/engine_data"
)

// ValidateJSONBObject validates a bounded JSON object using the
// same normalization and PostgreSQL numeric limits as persisted payloads.
func ValidateJSONBObject(encoded []byte, maxBytes int) (int, error) {
	if maxBytes <= 0 {
		return 0, domainengine.ErrInvalid
	}
	if len(encoded) > maxBytes {
		return 0, domainengine.ErrLimit
	}
	if !utf8.Valid(encoded) || !json.Valid(encoded) || !validJSONBUnicode(encoded) {
		return 0, domainengine.ErrInvalid
	}
	normalizedBytes, ok := estimateJSONBTextBytes(encoded, maxBytes)
	if !ok {
		return 0, domainengine.ErrLimit
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, domainengine.ErrInvalid
	}
	if _, ok := value.(map[string]any); !ok {
		return 0, domainengine.ErrInvalid
	}
	return normalizedBytes, nil
}

// estimateJSONBTextBytes bounds the text representation returned by PostgreSQL
// JSONB. JSONB adds separator spaces and expands numeric exponents to decimal
// notation; rejecting a document whose normalized form exceeds its configured
// limit keeps List responses bounded even for adversarial numeric literals.
func estimateJSONBTextBytes(encoded []byte, maxBytes int) (int, bool) {
	if maxBytes <= 0 || len(encoded) > maxBytes || !utf8.Valid(encoded) || !json.Valid(encoded) || !validJSONBUnicode(encoded) {
		return 0, false
	}
	total := int64(len(encoded))
	inString, escaped := false, false
	for i := 0; i < len(encoded); {
		char := encoded[i]
		if inString {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			i++
			continue
		}
		if char == '"' {
			inString = true
			i++
			continue
		}
		if char == ',' || char == ':' {
			total++
			i++
			if total > int64(maxBytes) {
				return 0, false
			}
			continue
		}
		if char == '-' || char >= '0' && char <= '9' {
			end := i + 1
			for end < len(encoded) && isJSONNumberByte(encoded[end]) {
				end++
			}
			numberBytes, ok := jsonbNumberTextBytes(encoded[i:end])
			if !ok {
				return 0, false
			}
			if numberBytes > end-i {
				total += int64(numberBytes - (end - i))
				if total > int64(maxBytes) {
					return 0, false
				}
			}
			i = end
			continue
		}
		i++
	}
	return int(total), total <= int64(maxBytes)
}

func validJSONBUnicode(encoded []byte) bool {
	inString := false
	for i := 0; i < len(encoded); i++ {
		if !inString {
			if encoded[i] == '"' {
				inString = true
			}
			continue
		}
		switch encoded[i] {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(encoded) {
				return false
			}
			if encoded[i+1] != 'u' {
				i++
				continue
			}
			code, next, ok := jsonUnicodeEscape(encoded, i)
			if !ok || code == 0 || code >= 0xDC00 && code <= 0xDFFF {
				return false
			}
			if code >= 0xD800 && code <= 0xDBFF {
				low, afterLow, valid := jsonUnicodeEscape(encoded, next)
				if !valid || low < 0xDC00 || low > 0xDFFF {
					return false
				}
				i = afterLow - 1
			} else {
				i = next - 1
			}
		}
	}
	return !inString
}

func jsonUnicodeEscape(encoded []byte, start int) (uint16, int, bool) {
	if start < 0 || start+6 > len(encoded) || encoded[start] != '\\' || encoded[start+1] != 'u' {
		return 0, 0, false
	}
	var code uint16
	for _, digit := range encoded[start+2 : start+6] {
		code <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			code |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			code |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			code |= uint16(digit-'A') + 10
		default:
			return 0, 0, false
		}
	}
	return code, start + 6, true
}

func isJSONNumberByte(value byte) bool {
	return value == '-' || value == '+' || value == '.' || value == 'e' || value == 'E' || value >= '0' && value <= '9'
}

func jsonbNumberTextBytes(value []byte) (int, bool) {
	if len(value) == 0 {
		return 0, false
	}
	sign := int64(0)
	if value[0] == '-' {
		sign = 1
		value = value[1:]
	}
	exponent := int64(0)
	if index := bytes.IndexAny(value, "eE"); index >= 0 {
		exponentText := value[index+1:]
		value = value[:index]
		if len(exponentText) == 0 {
			return 0, false
		}
		exponentSign := int64(1)
		if exponentText[0] == '+' || exponentText[0] == '-' {
			if exponentText[0] == '-' {
				exponentSign = -1
			}
			exponentText = exponentText[1:]
			if len(exponentText) == 0 {
				return 0, false
			}
		}
		firstDigit := 0
		for firstDigit < len(exponentText) && exponentText[firstDigit] == '0' {
			firstDigit++
		}
		exponentText = exponentText[firstDigit:]
		if len(exponentText) > 9 {
			return 0, false
		}
		for _, digit := range exponentText {
			exponent = exponent*10 + int64(digit-'0')
		}
		exponent *= exponentSign
	}
	integerDigits, fractionDigits := value, []byte(nil)
	if dot := bytes.IndexByte(value, '.'); dot >= 0 {
		integerDigits, fractionDigits = value[:dot], value[dot+1:]
	}
	leadingZeros, hasCoefficient := 0, false
	for i := 0; i < len(integerDigits)+len(fractionDigits); i++ {
		digit := byte(0)
		if i < len(integerDigits) {
			digit = integerDigits[i]
		} else {
			digit = fractionDigits[i-len(integerDigits)]
		}
		if !hasCoefficient && digit == '0' {
			leadingZeros++
			continue
		}
		hasCoefficient = true
	}
	coefficientBytes := len(integerDigits) + len(fractionDigits) - leadingZeros
	if !hasCoefficient {
		scale := int64(len(fractionDigits)) - exponent
		if scale <= 0 {
			return int(sign + 1), true
		}
		if scale > 16383 {
			return 0, false
		}
		return int(sign + 2 + scale), true
	}
	// Leading fractional zeros move the decimal point left relative to the
	// significant coefficient. For example, 0.001e2 normalizes to 0.1; using
	// only the raw integer-part width would incorrectly estimate it as 100.0.
	point := int64(len(integerDigits)) + exponent - int64(leadingZeros)
	scale := int64(len(fractionDigits)) - exponent
	if scale < 0 {
		scale = 0
	}
	if point > 131072 || scale > 16383 {
		return 0, false
	}
	length := sign
	if point > 0 {
		length += point
		if scale > 0 {
			length += 1 + scale
		}
	} else {
		length += 2 - point + int64(coefficientBytes)
	}
	if length > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(length), true
}
