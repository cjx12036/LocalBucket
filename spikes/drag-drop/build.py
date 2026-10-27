#!/usr/bin/env python3
"""Build disposable native drag/drop probes on macOS, including Win64 cross-build."""
from pathlib import Path
import hashlib
import io
import json
import os
import plistlib
import shutil
import subprocess
import tarfile
import tempfile
import struct
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parent
DIST = ROOT / 'dist'
VERSION = '0.47.0'
VC_URL = 'https://download.visualstudio.microsoft.com/download/pr/ebdab8e5-1d7b-4d9f-a11b-cbb1720c3b12/843068991DAAA1F73AD9F6239BCE4D0F6A07A51F18C37EA2A867E9BECA71295C/VC_redist.x64.exe'
VC_SHA256 = 'd1f4225df2cd877dbf130d5668a021dce3f94118455ff5ec952061c30afc9ce7'


def fetch_vcruntime() -> None:
    destination = ROOT / 'windows/vcruntime140.dll'
    if destination.exists() and hashlib.sha256(destination.read_bytes()).hexdigest() == VC_SHA256:
        return
    sevenzip = shutil.which('7zz') or shutil.which('7z')
    if not sevenzip:
        raise RuntimeError('Build-time 7-Zip is needed to extract the Microsoft runtime (not required by users).')
    data = urllib.request.urlopen(VC_URL, timeout=90).read()
    with tempfile.TemporaryDirectory(prefix='localbucket-vcrt-') as tmp:
        work = Path(tmp)
        offset = 0
        while True:
            offset = data.find(b'MSCF', offset)
            if offset < 0:
                break
            position = offset
            offset += 4
            size = struct.unpack_from('<I', data, position + 8)[0]
            if not 36 < size <= len(data) - position:
                continue
            cabinet = work / f'{position}.cab'
            cabinet.write_bytes(data[position:position + size])
            extracted = work / f'extracted-{position}'
            subprocess.run([sevenzip, 'x', '-y', f'-o{extracted}', str(cabinet)], check=True, stdout=subprocess.DEVNULL)
            for payload in extracted.iterdir():
                if payload.read_bytes()[:4] != b'MSCF':
                    continue
                target = work / ('payload-' + payload.name)
                subprocess.run([sevenzip, 'x', '-y', f'-o{target}', str(payload)], check=True, stdout=subprocess.DEVNULL)
                runtime = target / 'vcruntime140.dll_amd64'
                if runtime.exists() and hashlib.sha256(runtime.read_bytes()).hexdigest() == VC_SHA256:
                    shutil.copy2(runtime, destination)
                    return
    raise RuntimeError('Pinned Microsoft x64 runtime was not found or its SHA256 did not match.')


def fetch_dufs(target: str, destination: Path, archive_type: str) -> None:
    if destination.exists():
        return
    name = f'dufs-v{VERSION}-{target}.{archive_type}'
    url = f'https://github.com/sigoden/dufs/releases/download/v{VERSION}/{name}'
    data = urllib.request.urlopen(url, timeout=90).read()
    if archive_type == 'zip':
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            member = next(n for n in archive.namelist() if n.endswith('dufs.exe'))
            binary = archive.read(member)
    else:
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
            member = next(n for n in archive if n.name.rsplit('/', 1)[-1] == 'dufs')
            binary = archive.extractfile(member).read()
    destination.write_bytes(binary)
    destination.chmod(0o755)


def main() -> None:
    DIST.mkdir(exist_ok=True)
    vendor = ROOT / 'vendor'
    vendor.mkdir(exist_ok=True)
    fetch_dufs('aarch64-apple-darwin', vendor / 'dufs-macos', 'tar.gz')
    fetch_dufs('x86_64-pc-windows-msvc', ROOT / 'windows/dufs.exe', 'zip')
    fetch_vcruntime()
    app = DIST / 'LocalBucket Drag Spike.app'
    macos = app / 'Contents/MacOS'
    resources = app / 'Contents/Resources'
    macos.mkdir(parents=True, exist_ok=True)
    resources.mkdir(parents=True, exist_ok=True)
    subprocess.run(['xcrun', 'swiftc', str(ROOT / 'macos/DragSpike.swift'), '-O',
                    '-framework', 'AppKit', '-framework', 'UniformTypeIdentifiers',
                    '-o', str(macos / 'DragSpike')], check=True)
    shutil.copy2(vendor / 'dufs-macos', resources / 'dufs')
    with (app / 'Contents/Info.plist').open('wb') as stream:
        plistlib.dump({
            'CFBundleExecutable': 'DragSpike',
            'CFBundleIdentifier': 'local.localbucket.dragspike',
            'CFBundleName': 'LocalBucket Drag Spike',
            'CFBundlePackageType': 'APPL',
            'CFBundleShortVersionString': '0.0.1',
            'LSMinimumSystemVersion': '15.0',
            'NSHighResolutionCapable': True,
            'NSLocalNetworkUsageDescription': 'Test transfers between your own local computers.',
        }, stream)
    for name in ['LICENSE-MIT', 'LICENSE-APACHE']:
        license_file = vendor / name
        if not license_file.exists():
            license_file.write_bytes(urllib.request.urlopen(
                f'https://raw.githubusercontent.com/sigoden/dufs/v{VERSION}/{name}').read())
        shutil.copy2(license_file, resources / name)
    subprocess.run(['codesign', '--force', '--deep', '--sign', '-', str(app)], check=True)
    go = shutil.which('go') or '/opt/homebrew/bin/go'
    exe = DIST / 'LocalBucketDragSpike-win-x64.exe'
    subprocess.run([go, 'build', '-trimpath', '-ldflags=-H windowsgui -s -w',
                    '-o', str(exe), '.'], cwd=ROOT / 'windows', check=True,
                   env={**os.environ, 'GOOS': 'windows', 'GOARCH': 'amd64', 'CGO_ENABLED': '0'})
    with zipfile.ZipFile(DIST / 'LocalBucketDragSpike-windows-test.zip', 'w', zipfile.ZIP_DEFLATED) as archive:
        archive.write(exe, exe.name)
        archive.write(ROOT / 'README.md', '测试说明.md')
        for name in ['LICENSE-MIT', 'LICENSE-APACHE']:
            archive.write(vendor / name, 'licenses/' + name)
        archive.write(vendor / 'MICROSOFT-RUNTIME-NOTICE.txt', 'licenses/MICROSOFT-RUNTIME-NOTICE.txt')
        for item in (ROOT / 'fixtures/input').rglob('*'):
            if item.is_file() and not any(part.startswith('.') for part in item.relative_to(ROOT / 'fixtures/input').parts):
                archive.write(item, Path('测试素材') / item.relative_to(ROOT / 'fixtures/input'))
    subprocess.run(['ditto', '-c', '-k', '--sequesterRsrc', '--keepParent', str(app),
                    str(DIST / 'LocalBucketDragSpike-mac-arm64.zip')], check=True)
    manifest = {}
    for item in DIST.iterdir():
        if item.is_file() and item.name != 'SHA256.json':
            manifest[item.name] = {'bytes': item.stat().st_size,
                                   'sha256': hashlib.sha256(item.read_bytes()).hexdigest()}
    (DIST / 'SHA256.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()
