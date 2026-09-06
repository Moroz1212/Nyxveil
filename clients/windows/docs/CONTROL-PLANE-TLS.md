# Control Plane TLS (Windows GUI)

Production GUI uses **SystemTrust** for Control Plane HTTPS (no custom TrustAll,
no InsecureSkipVerify).

Live E2E therefore requires a **publicly trusted** Control Plane certificate
(hostname match), in addition to publicly trusted node TLS + catalog SPKI pin.

Private-CA CP is a future explicit trust mode — not implemented in this candidate.
