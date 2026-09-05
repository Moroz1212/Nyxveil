package server

import (
	"testing"

	"github.com/nyxveil/nvp/core/controlplane/model"
)

func TestLicenseTokenValid(t *testing.T) {
	lic := &model.LicenseRecord{LicenseID: "nyx_lic_test1", Secret: "test-secret"}
	if !licenseTokenValid(lic, "nyx_lic_test1:test-secret") {
		t.Fatal("expected valid id:secret")
	}
	if licenseTokenValid(lic, "nyx_lic_test1") {
		t.Fatal("token without secret must fail")
	}
	if licenseTokenValid(lic, "nyx_lic_test1:wrong") {
		t.Fatal("wrong secret must fail")
	}
	empty := &model.LicenseRecord{LicenseID: "nyx_lic_test1", Secret: ""}
	if licenseTokenValid(empty, "nyx_lic_test1") {
		t.Fatal("empty stored secret must fail closed")
	}
	if licenseTokenValid(empty, "nyx_lic_test1:") {
		t.Fatal("empty stored secret must fail even with empty token secret")
	}
}
