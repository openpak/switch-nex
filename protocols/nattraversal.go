// NAT traversal (0x03) — probe bookkeeping. The measured servers answered
// these probe methods empty; the actual NAT check is Pia's, served by the
// nncs1/nncs2 responders on the two distinct IPs (SPEC.md), not by RMC.
package protocols

import (
	"github.com/openpak/switch-nex/prudplite"
	"github.com/openpak/switch-nex/rmc"
)

func natTraversalHandler(env *Env, c *prudplite.Connection, msg *rmc.RMCMessage) []byte {
	switch msg.MethodID {
	case 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07:
		// RequestProbeInitiation, InitiateProbe, RequestProbeInitiationExt,
		// ReportNATTraversalResult, ReportNATProperties,
		// GetRelaySignatureKey, ReportNATTraversalResultDetail
		return success(msg, nil)
	default:
		return notImplemented(env, msg)
	}
}
