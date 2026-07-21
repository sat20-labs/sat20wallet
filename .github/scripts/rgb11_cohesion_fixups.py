from pathlib import Path

api_types = Path("sdk/wallet/rgb11/api_types.go")
manager = Path("sdk/wallet/rgb11_manager.go")

api_text = api_types.read_text(encoding="utf-8")
if '"errors"' not in api_text:
    api_text = api_text.replace('import (\n', 'import (\n\t"errors"\n', 1)

error_decl = 'var ErrRGB11Rejected = errors.New("RGB11 allocation rejected by issuer policy")\n\n'
if "var ErrRGB11Rejected" not in api_text:
    marker = "// RGB11Output is the serializable projection view exposed to UI clients.\n"
    if marker not in api_text:
        raise SystemExit("api_types.go insertion marker not found")
    api_text = api_text.replace(marker, error_decl + marker, 1)
api_types.write_text(api_text, encoding="utf-8")

manager_text = manager.read_text(encoding="utf-8")
old = '\tErrRGB11Rejected              = errors.New("RGB11 allocation rejected by issuer policy")'
new = '\tErrRGB11Rejected              = rgb11wallet.ErrRGB11Rejected'
if old in manager_text:
    manager_text = manager_text.replace(old, new, 1)
elif new not in manager_text:
    raise SystemExit("rgb11_manager.go error alias marker not found")
manager.write_text(manager_text, encoding="utf-8")
