#!/usr/bin/env python3
"""Build LocalBucket on macOS: Apple Silicon app and portable Windows x64 EXE."""
from pathlib import Path
import hashlib, importlib.util, json, os, plistlib, shlex, shutil, struct, subprocess, zipfile

APP=Path(__file__).resolve().parent
REPO=APP.parent
DIST=APP/'dist'
VERSION='0.2.0'
GO=shutil.which('go') or '/opt/homebrew/bin/go'

def run(args,**kwargs):
    subprocess.run([str(a) for a in args],check=True,**kwargs)

def sign_windows(path):
    command=os.environ.get('LOCALBUCKET_WINDOWS_SIGN_COMMAND')
    if command:
        run([*shlex.split(command),str(path)])

def icons():
    source=REPO/'assets/icons/localbucket-concept-v1.png'
    iconset=DIST/'LocalBucket.iconset'
    iconset.mkdir(exist_ok=True)
    sizes=[]
    for size in [16,32,64,128,256,512,1024]:
        p=iconset/f'icon_{size}x{size}.png'
        run(['sips','-z',str(size),str(size),source,'--out',p],stdout=subprocess.DEVNULL)
        sizes.append((size,p))
    for size in [16,32,128,256,512]:
        shutil.copy2(iconset/f'icon_{size*2}x{size*2}.png',iconset/f'icon_{size}x{size}@2x.png')
    run(['iconutil','-c','icns',iconset,'-o',APP/'resources/LocalBucket.icns'])
    entries=[(size,p.read_bytes()) for size,p in sizes if size<=256]
    offset=6+16*len(entries);header=struct.pack('<HHH',0,1,len(entries));images=b''
    for size,data in entries:
        header+=struct.pack('<BBBBHHII',size%256,size%256,0,0,1,32,len(data),offset)
        images+=data;offset+=len(data)
    (APP/'resources/LocalBucket.ico').write_bytes(header+images)

def main():
    DIST.mkdir(exist_ok=True);(APP/'resources').mkdir(exist_ok=True)
    spec=importlib.util.spec_from_file_location('spike_build',REPO/'spikes/drag-drop/build.py')
    spike=importlib.util.module_from_spec(spec);spec.loader.exec_module(spike)
    spike.fetch_dufs('aarch64-apple-darwin',REPO/'spikes/drag-drop/vendor/dufs-macos','tar.gz')
    spike.fetch_dufs('x86_64-pc-windows-msvc',APP/'cmd/localbucket/dufs.exe','zip')
    spike.fetch_vcruntime()
    shutil.copy2(REPO/'spikes/drag-drop/windows/vcruntime140.dll',APP/'cmd/localbucket/vcruntime140.dll')
    # Sign inner executable before embedding, then sign the final portable EXE.
    sign_windows(APP/'cmd/localbucket/dufs.exe')
    icons()
    resource={
        'RT_GROUP_ICON':{'#1':{'0000':'LocalBucket.ico'}},
        'RT_MANIFEST':{'#1':{'0409':{'identity':{'name':'LocalBucket','version':'0.2.0.0'},'minimum-os':'win10','execution-level':'','dpi-awareness':'per monitor v2','use-common-controls-v6':True,'long-path-aware':True}}},
        'RT_VERSION':{'#1':{'0409':{'fixed':{'file_version':'0.2.0.0','product_version':'0.2.0.0'},'info':{'0409':{'ProductName':'LocalBucket','FileDescription':'LocalBucket LAN file sharing','FileVersion':VERSION,'ProductVersion':VERSION,'OriginalFilename':'LocalBucket.exe'}}}}}
    }
    (APP/'resources/winres.json').write_text(json.dumps(resource,indent=2)+'\n')
    run([GO,'run','github.com/tc-hib/go-winres@v0.3.3','make','--in',APP/'resources/winres.json','--out',APP/'cmd/localbucket/rsrc','--arch','amd64'],cwd=APP)
    exe=DIST/'LocalBucket-win-x64.exe'
    run([GO,'build','-trimpath','-ldflags=-H windowsgui -s -w','-o',exe,'./cmd/localbucket'],cwd=APP,env={**os.environ,'GOOS':'windows','GOARCH':'amd64','CGO_ENABLED':'0'})
    sign_windows(exe)
    bundle=DIST/'LocalBucket.app';macos=bundle/'Contents/MacOS';res=bundle/'Contents/Resources'
    macos.mkdir(parents=True,exist_ok=True);res.mkdir(parents=True,exist_ok=True)
    run([GO,'build','-trimpath','-ldflags=-s -w','-o',res/'localbucket-node','./cmd/node'],cwd=APP,env={**os.environ,'GOOS':'darwin','GOARCH':'arm64','CGO_ENABLED':'0'})
    run(['xcrun','swiftc','-O',APP/'macos/Trash.swift','-o',res/'localbucket-trash'])
    run(['xcrun','swiftc','-O','-framework','AppKit','-framework','UniformTypeIdentifiers',APP/'macos/LocalBucket.swift','-o',macos/'LocalBucket'])
    shutil.copy2(REPO/'spikes/drag-drop/vendor/dufs-macos',res/'dufs')
    shutil.copy2(APP/'resources/LocalBucket.icns',res/'LocalBucket.icns')
    for name in ['LICENSE-MIT','LICENSE-APACHE','MICROSOFT-RUNTIME-NOTICE.txt']:
        shutil.copy2(REPO/'spikes/drag-drop/vendor'/name,res/name)
    with (bundle/'Contents/Info.plist').open('wb') as stream:
        plistlib.dump({'CFBundleExecutable':'LocalBucket','CFBundleIdentifier':'local.localbucket.app','CFBundleName':'LocalBucket','CFBundleDisplayName':'LocalBucket','CFBundlePackageType':'APPL','CFBundleShortVersionString':VERSION,'CFBundleVersion':'1','CFBundleIconFile':'LocalBucket.icns','LSMinimumSystemVersion':'15.0','NSHighResolutionCapable':True,'NSLocalNetworkUsageDescription':'自动寻找同一局域网的电脑，以存放和复制共享文件。','NSBonjourServices':['_localbucket._tcp']},stream)
    run(['codesign','--force','--deep','--sign',os.environ.get('LOCALBUCKET_MAC_SIGN_IDENTITY','-'),bundle])
    run(['ditto','-c','-k','--sequesterRsrc','--keepParent',bundle,DIST/'LocalBucket-mac-arm64.zip'])
    with zipfile.ZipFile(DIST/'LocalBucket-windows.zip','w',zipfile.ZIP_DEFLATED) as z:
        z.write(exe,'LocalBucket.exe');z.write(APP/'README.md','使用说明.md')
        for name in ['LICENSE-MIT','LICENSE-APACHE','MICROSOFT-RUNTIME-NOTICE.txt']:
            z.write(REPO/'spikes/drag-drop/vendor'/name,'licenses/'+name)
    manifest={}
    for p in [exe,DIST/'LocalBucket-mac-arm64.zip',DIST/'LocalBucket-windows.zip']:
        manifest[p.name]={'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
    (DIST/'SHA256.json').write_text(json.dumps(manifest,indent=2)+'\n')
    print(json.dumps({'version':VERSION,'windows_signing_requested':bool(os.environ.get('LOCALBUCKET_WINDOWS_SIGN_COMMAND')),'assets':manifest},indent=2))

if __name__=='__main__':main()
