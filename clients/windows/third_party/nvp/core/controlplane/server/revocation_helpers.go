package server

import (
	"github.com/nyxveil/nvp/core/auth/ticket"
	"github.com/nyxveil/nvp/core/controlplane/api"
)

func mergeRevocation(mem *ticket.MemoryRevocation, base api.RevocationListResponse) api.RevocationListResponse {
	if mem == nil {
		return base
	}
	jtis, licenses, devices := mem.Snapshot()
	out := base
	for _, v := range jtis {
		out.RevokedJTIs = appendUnique(out.RevokedJTIs, v)
	}
	for _, v := range licenses {
		out.RevokedLicenses = appendUnique(out.RevokedLicenses, v)
	}
	for _, v := range devices {
		out.RevokedDevices = appendUnique(out.RevokedDevices, v)
	}
	return out
}
