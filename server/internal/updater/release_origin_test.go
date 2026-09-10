package updater

import "testing"

func TestProductionReleaseOrigin(t *testing.T) {
	if err := ValidateReleaseURL(ManifestURLForVersion("1.1.12")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"http://github.com/Moroz1212/Nyxveil/releases/download/server-v1.1.12/install.sh",
		"https://github.com/evil/Nyxveil/releases/download/server-v1.1.12/install.sh",
		"https://github.com.evil/Moroz1212/Nyxveil/releases/download/server-v1.1.12/install.sh",
		"https://user@github.com/Moroz1212/Nyxveil/releases/download/server-v1.1.12/install.sh",
		ManifestURLForVersion("1.1.12") + "?redirect=evil",
	} {
		if ValidateReleaseURL(raw) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
