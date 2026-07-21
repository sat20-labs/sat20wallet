from pathlib import Path
import subprocess

api_types = Path("sdk/wallet/rgb11/api_types.go")
backup_codec = Path("sdk/wallet/rgb11/backup_codec.go")
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

backup_text = backup_codec.read_text(encoding="utf-8")
public_magic = (
    "\n// SnapshotPayloadMagic and SnapshotEnvelopeMagic identify the stable RGB11 "
    "wallet recovery codecs.\n"
    "const (\n"
    "\tSnapshotPayloadMagic  = rgb11SnapshotPayloadMagic\n"
    "\tSnapshotEnvelopeMagic = rgb11SnapshotEnvelopeMagic\n"
    ")\n"
)
if "\tSnapshotPayloadMagic  =" not in backup_text:
    marker = ")\n\nfunc EncodeAutoBackupPolicy"
    if marker not in backup_text:
        raise SystemExit("backup_codec.go constant marker not found")
    backup_text = backup_text.replace(marker, ")" + public_magic + "\nfunc EncodeAutoBackupPolicy", 1)
backup_codec.write_text(backup_text, encoding="utf-8")

manager_text = manager.read_text(encoding="utf-8")
if '"github.com/btcsuite/btcd/btcec/v2"' not in manager_text:
    marker = '\t"github.com/btcsuite/btcd/btcutil/psbt"'
    if marker not in manager_text:
        raise SystemExit("rgb11_manager.go btcec import marker not found")
    manager_text = manager_text.replace(
        marker,
        '\t"github.com/btcsuite/btcd/btcec/v2"\n' + marker,
        1,
    )

old = '\tErrRGB11Rejected              = errors.New("RGB11 allocation rejected by issuer policy")'
new = '\tErrRGB11Rejected              = rgb11wallet.ErrRGB11Rejected'
if old in manager_text:
    manager_text = manager_text.replace(old, new, 1)
elif new not in manager_text:
    raise SystemExit("rgb11_manager.go error alias marker not found")
manager.write_text(manager_text, encoding="utf-8")

# Tests in package wallet should exercise implementation-only helpers through
# the dedicated rgb11Manager rather than reintroducing private methods on the
# outer Manager.
api_test = Path("sdk/wallet/rgb11_address_api_test.go")
api_test_text = api_test.read_text(encoding="utf-8")
setup = "\tmanager := &Manager{}\n"
setup_with_component = setup + "\tmanager.rgbManager = &rgb11Manager{Manager: manager}\n"
if setup_with_component not in api_test_text:
    if setup not in api_test_text:
        raise SystemExit("rgb11_address_api_test.go manager setup marker not found")
    api_test_text = api_test_text.replace(setup, setup_with_component, 1)
api_test_text = api_test_text.replace(
    "manager.configureRGB11AddressRetention(",
    "manager.rgbManager.configureRGB11AddressRetention(",
)
api_test.write_text(api_test_text, encoding="utf-8")

manager_test = Path("sdk/wallet/rgb11_manager_test.go")
manager_test_text = manager_test.read_text(encoding="utf-8")
for method in ("rgb11CarrierBinding", "ownsRGB11Carrier", "buildRGB11WitnessTx"):
    manager_test_text = manager_test_text.replace(
        f"manager.{method}(",
        f"manager.rgbManager.{method}(",
    )
manager_test.write_text(manager_test_text, encoding="utf-8")

sync_test = Path("sdk/wallet/rgb11_sync_test.go")
sync_test_text = sync_test.read_text(encoding="utf-8")
for receiver in ("deviceA", "deviceB", "manager", "newWallet"):
    for method in (
        "requireLatestRGB11WalletState",
        "autoBackupRGB11AfterMutation",
        "waitForRGB11AutoBackup",
    ):
        sync_test_text = sync_test_text.replace(
            f"{receiver}.{method}(",
            f"{receiver}.rgbManager.{method}(",
        )
sync_test_text = sync_test_text.replace(
    "rgb11SnapshotPayloadMagic",
    "rgb11wallet.SnapshotPayloadMagic",
)
sync_test_text = sync_test_text.replace(
    "rgb11SnapshotEnvelopeMagic",
    "rgb11wallet.SnapshotEnvelopeMagic",
)
sync_test.write_text(sync_test_text, encoding="utf-8")

subprocess.run(
    [
        "gofmt",
        "-w",
        str(api_types),
        str(backup_codec),
        str(manager),
        str(api_test),
        str(manager_test),
        str(sync_test),
    ],
    check=True,
)

# Temporary diagnostics: keep the test log in the transformed-source artifact,
# but exclude it from git so the final source commit remains clean.
diagnostic = Path("sdk/wallet/rgb11/.cohesion-tests.log")
exclude = Path(".git/info/exclude")
exclude.parent.mkdir(parents=True, exist_ok=True)
exclude_text = exclude.read_text(encoding="utf-8") if exclude.exists() else ""
exclude_rule = "sdk/wallet/rgb11/.cohesion-tests.log"
if exclude_rule not in exclude_text.splitlines():
    with exclude.open("a", encoding="utf-8") as stream:
        if exclude_text and not exclude_text.endswith("\n"):
            stream.write("\n")
        stream.write(exclude_rule + "\n")
with diagnostic.open("wb") as stream:
    result = subprocess.run(
        ["go", "test", "./wallet/...", "-count=1"],
        cwd="sdk",
        stdout=stream,
        stderr=subprocess.STDOUT,
        check=False,
    )
print(f"cohesion diagnostic tests exit={result.returncode}")
