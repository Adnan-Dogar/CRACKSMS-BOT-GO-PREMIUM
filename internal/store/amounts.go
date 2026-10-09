package store

import "math"

// validAmount rejects negative, NaN and infinite money values. PostgreSQL
// numeric accepts NaN, and a NaN balance would defeat every later balance check.
func validAmount(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// ValidAmount reports whether value is a finite, non-negative money amount.
func ValidAmount(value float64) bool { return validAmount(value) }

// InputError is a validation failure whose message is safe to show the user.
type InputError struct{ Message string }

func (e *InputError) Error() string { return e.Message }

func invalidInput(message string) error { return &InputError{Message: message} }
