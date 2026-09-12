import pathlib
p = pathlib.Path("licensing/scripts/real-cp-button-update-e2e.ps1")
b = p.read_bytes()
print("has_bom", b[:3] == b"\xef\xbb\xbf")
needle = "Войти".encode("utf-8")
idx = b.find(needle)
print("utf8_idx", idx)
if idx >= 0:
    print(b[idx-30:idx+40])
needle2 = "control-plane-update".encode()
print("testid", b.find(needle2))
