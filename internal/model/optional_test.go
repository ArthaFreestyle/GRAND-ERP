package model

import (
	"encoding/json"
	"testing"
)

// The three states have to survive JSON decoding, because that is the only place
// "key was absent" can still be observed.
func TestOptionalDecodesThreeStates(t *testing.T) {
	type body struct {
		Telepon Optional[string] `json:"telepon"`
	}

	t.Run("absent key", func(t *testing.T) {
		var decoded body
		if err := json.Unmarshal([]byte(`{}`), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if decoded.Telepon.Present {
			t.Error("Present = true for a key that was not in the body")
		}

		if decoded.Telepon.Clears() {
			t.Error("Clears() = true for an absent key; only an explicit null clears")
		}

		if decoded.Telepon.Set() {
			t.Error("Set() = true for an absent key")
		}
	})

	t.Run("explicit null", func(t *testing.T) {
		var decoded body
		if err := json.Unmarshal([]byte(`{"telepon":null}`), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if !decoded.Telepon.Present {
			t.Error("Present = false for an explicit null")
		}

		if decoded.Telepon.Value != nil {
			t.Errorf("Value = %q for an explicit null, want nil", *decoded.Telepon.Value)
		}

		if !decoded.Telepon.Clears() {
			t.Error("Clears() = false for an explicit null")
		}
	})

	t.Run("explicit value", func(t *testing.T) {
		var decoded body
		if err := json.Unmarshal([]byte(`{"telepon":"0800-1111"}`), &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if !decoded.Telepon.Set() {
			t.Fatal("Set() = false for a supplied value")
		}

		if *decoded.Telepon.Value != "0800-1111" {
			t.Errorf("Value = %q, want 0800-1111", *decoded.Telepon.Value)
		}

		if decoded.Telepon.Clears() {
			t.Error("Clears() = true for a supplied value")
		}
	})
}

func TestOptionalRejectsWrongType(t *testing.T) {
	type body struct {
		IsAktif Optional[bool] `json:"is_aktif"`
	}

	var decoded body
	if err := json.Unmarshal([]byte(`{"is_aktif":"ya"}`), &decoded); err == nil {
		t.Error("expected an error decoding a string into Optional[bool]")
	}
}
