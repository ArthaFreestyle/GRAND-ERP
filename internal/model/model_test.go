package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// The envelope is what the client actually receives, so the "empty list is []"
// promise has to hold here and not merely for the slice on its own. With
// omitempty on Data the key vanishes and response.data is undefined, which breaks
// callers on exactly the page that has no rows.
func TestWebResponseAlwaysCarriesData(t *testing.T) {
	response := WebResponse[[]RuangResponse]{
		Data:   []RuangResponse{},
		Paging: &PageMetadata{Page: 1, Size: 20},
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if !strings.Contains(string(encoded), `"data":[]`) {
		t.Errorf("encoded = %s, want it to contain \"data\":[]", encoded)
	}
}

func TestPageRequestNormalize(t *testing.T) {
	cases := []struct {
		name               string
		request            PageRequest
		wantPage, wantSize int
		wantOffset         int
	}{
		{name: "defaults are filled in", request: PageRequest{}, wantPage: 1, wantSize: 20, wantOffset: 0},
		{name: "page 3 of 20", request: PageRequest{Page: 3, Size: 20}, wantPage: 3, wantSize: 20, wantOffset: 40},
		{name: "size is capped", request: PageRequest{Page: 1, Size: 500}, wantPage: 1, wantSize: 100, wantOffset: 0},
		{name: "negative page", request: PageRequest{Page: -2, Size: 10}, wantPage: 1, wantSize: 10, wantOffset: 0},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := testCase.request
			request.Normalize()

			if request.Page != testCase.wantPage || request.Size != testCase.wantSize {
				t.Errorf("page/size = %d/%d, want %d/%d",
					request.Page, request.Size, testCase.wantPage, testCase.wantSize)
			}

			if got := request.Offset(); got != testCase.wantOffset {
				t.Errorf("Offset() = %d, want %d", got, testCase.wantOffset)
			}
		})
	}
}
