package nodetls

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"golang.org/x/crypto/acme"
)

func TestIsAlreadyRegistered_ErrAccountAlreadyExists(t *testing.T) {
	if !isAlreadyRegistered(acme.ErrAccountAlreadyExists) {
		t.Fatal("sentinel acme.ErrAccountAlreadyExists must be accepted")
	}
	wrapped := fmt.Errorf("nodetls: ACME register: %w", acme.ErrAccountAlreadyExists)
	if !isAlreadyRegistered(wrapped) {
		t.Fatal("wrapped ErrAccountAlreadyExists must be accepted via errors.Is")
	}
}

func TestIsAlreadyRegistered_HTTPConflict(t *testing.T) {
	err409 := &acme.Error{StatusCode: http.StatusConflict, ProblemType: "urn:ietf:params:acme:error:invalidContact", Detail: "conflict"}
	if !isAlreadyRegistered(err409) {
		t.Fatal("HTTP 409 *acme.Error must remain compatible")
	}
	wrapped := fmt.Errorf("register: %w", err409)
	if !isAlreadyRegistered(wrapped) {
		t.Fatal("wrapped HTTP 409 *acme.Error must be accepted via errors.As")
	}
}

func TestIsAlreadyRegistered_OtherErrorsRejected(t *testing.T) {
	cases := []error{
		errors.New("acme: account already exists"), // plain text lookalike — must NOT match
		&acme.Error{StatusCode: http.StatusForbidden, Detail: "forbidden"},
		&acme.Error{StatusCode: http.StatusInternalServerError, Detail: "boom"},
		acme.ErrNoAccount,
		fmt.Errorf("nodetls: ACME order: timeout"),
		nil,
	}
	for _, err := range cases {
		if isAlreadyRegistered(err) {
			t.Fatalf("unexpected accept for %v", err)
		}
	}
}
