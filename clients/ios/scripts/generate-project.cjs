// Deterministic project generator; the generated .xcodeproj is checked in.
const fs = require('fs'), path = require('path'), crypto = require('crypto');
const root = path.resolve(__dirname, '..');
const objects = {};
const id = name => crypto.createHash('sha256').update(name).digest('hex').slice(0,24).toUpperCase();
const ref = name => ({ref:id(name)});
const add = (name, value) => { objects[id(name)] = value; return ref(name); };
const list = dir => fs.readdirSync(path.join(root,dir)).filter(x=>/\.(swift|m)$/.test(x)).map(x=>`${dir}/${x}`);
const files = [...list('Nyxveil'),...list('NyxveilShared'),...list('NyxveilTunnel'),...list('Tests')];
for (const p of [...files,'NyxveilShared/CoreShim.h','NyxveilShared/NyxveilShared.h','Config/Signing.xcconfig'])
  add(p,{isa:'PBXFileReference',lastKnownFileType:p.endsWith('.swift')?'sourcecode.swift':p.endsWith('.m')?'sourcecode.c.objc':p.endsWith('.h')?'sourcecode.c.h':'text.xcconfig',path:p,sourceTree:'<group>'});
add('nvp',{isa:'PBXFileReference',lastKnownFileType:'wrapper.xcframework',path:'Frameworks/Nvp.xcframework',sourceTree:'<group>'});
const targets=['Nyxveil','NyxveilTunnel','NyxveilShared','NyxveilTests'];
const productExt=['app','appex','framework','xctest'];
const productType=['application','app-extension','framework','bundle.unit-test'];
targets.forEach((t,i)=>add(t+'Product',{isa:'PBXFileReference',explicitFileType:['wrapper.application','wrapper.app-extension','wrapper.framework','wrapper.cfbundle'][i],path:`${t}.${productExt[i]}`,sourceTree:'BUILT_PRODUCTS_DIR'}));
const buildFile=(name, file, settings)=>add(name,{isa:'PBXBuildFile',fileRef:file,...(settings?{settings}:{})});
for(const t of targets) {
  const dir=t==='NyxveilTests'?'Tests':t;
  const sourceFiles=files.filter(p=>p.startsWith(dir+'/')).map(p=>buildFile(t+p,ref(p)));
  add(t+'Sources',{isa:'PBXSourcesBuildPhase',buildActionMask:2147483647,files:sourceFiles,runOnlyForDeploymentPostprocessing:0});
  add(t+'Resources',{isa:'PBXResourcesBuildPhase',buildActionMask:2147483647,files:[],runOnlyForDeploymentPostprocessing:0});
  const link= t==='NyxveilShared'?ref('nvp'):ref('NyxveilSharedProduct');
  add(t+'Frameworks',{isa:'PBXFrameworksBuildPhase',buildActionMask:2147483647,files:[buildFile(t+'Link',link)],runOnlyForDeploymentPostprocessing:0});
  const dependencies=[];
  for (const dep of t==='Nyxveil'?['NyxveilShared','NyxveilTunnel']:t==='NyxveilShared'?[]:['NyxveilShared']) {
    const proxy=add(t+dep+'Proxy',{isa:'PBXContainerItemProxy',containerPortal:ref('Project'),proxyType:1,remoteGlobalIDString:ref(dep+'Target'),remoteInfo:dep});
    dependencies.push(add(t+dep+'Dependency',{isa:'PBXTargetDependency',target:ref(dep+'Target'),targetProxy:proxy}));
  }
  const phases=[ref(t+'Sources'),ref(t+'Frameworks'),ref(t+'Resources')];
  if(t==='NyxveilShared') {
    add('Headers',{isa:'PBXHeadersBuildPhase',buildActionMask:2147483647,files:['CoreShim.h','NyxveilShared.h'].map(h=>buildFile(h+'Header',ref('NyxveilShared/'+h),{ATTRIBUTES:['Public']})),runOnlyForDeploymentPostprocessing:0});
    phases.unshift(ref('Headers'));
  }
  if(t==='Nyxveil') {
    add('EmbedExtension',{isa:'PBXCopyFilesBuildPhase',buildActionMask:2147483647,dstPath:'',dstSubfolderSpec:13,name:'Embed App Extensions',files:[buildFile('EmbedTunnel',ref('NyxveilTunnelProduct'),{ATTRIBUTES:['RemoveHeadersOnCopy']})],runOnlyForDeploymentPostprocessing:0});
    phases.push(ref('EmbedExtension'));
  }
  if(t==='Nyxveil'||t==='NyxveilTests') {
    add(t+'EmbedShared',{isa:'PBXCopyFilesBuildPhase',buildActionMask:2147483647,dstPath:'',dstSubfolderSpec:10,name:'Embed Frameworks',files:[buildFile(t+'EmbedFramework',ref('NyxveilSharedProduct'),{ATTRIBUTES:['CodeSignOnCopy','RemoveHeadersOnCopy']})],runOnlyForDeploymentPostprocessing:0});
    phases.push(ref(t+'EmbedShared'));
  }
  for(const cfg of ['Debug','Release']) {
    const settings={PRODUCT_NAME:'$(TARGET_NAME)',SWIFT_VERSION:'5.0',CODE_SIGN_STYLE:'Automatic',CURRENT_PROJECT_VERSION:'1',MARKETING_VERSION:'0.1.0',
      PRODUCT_BUNDLE_IDENTIFIER:t==='Nyxveil'?'$(APP_BUNDLE_IDENTIFIER)':t==='NyxveilTunnel'?'$(TUNNEL_BUNDLE_IDENTIFIER)':`$(APP_BUNDLE_IDENTIFIER).${t==='NyxveilShared'?'shared':'tests'}`,
      GENERATE_INFOPLIST_FILE:'YES',SKIP_INSTALL:t==='Nyxveil'?'NO':'YES',
      LD_RUNPATH_SEARCH_PATHS:['$(inherited)','@executable_path/Frameworks','@executable_path/../../Frameworks','@loader_path/Frameworks'],
      TARGETED_DEVICE_FAMILY:'1,2',APPLICATION_EXTENSION_API_ONLY:t==='Nyxveil'?'NO':'YES'};
    if(t==='Nyxveil'||t==='NyxveilTunnel') {
      settings.INFOPLIST_FILE=`${t}/Info.plist`;
      settings.CODE_SIGN_ENTITLEMENTS=`${t}/${t}.entitlements`;
    }
    if(t==='Nyxveil') { settings.INFOPLIST_KEY_UILaunchScreen_Generation='YES'; settings.INFOPLIST_KEY_UISupportedInterfaceOrientations='UIInterfaceOrientationPortrait UIInterfaceOrientationPortraitUpsideDown UIInterfaceOrientationLandscapeLeft UIInterfaceOrientationLandscapeRight'; }
    if(t==='NyxveilShared') { settings.DEFINES_MODULE='YES'; settings.INSTALL_PATH='$(LOCAL_LIBRARY_DIR)/Frameworks'; settings.OTHER_LDFLAGS=['$(inherited)','-lresolv']; }
    add(t+cfg,{isa:'XCBuildConfiguration',name:cfg,buildSettings:settings});
  }
  add(t+'Configs',{isa:'XCConfigurationList',buildConfigurations:['Debug','Release'].map(c=>ref(t+c)),defaultConfigurationIsVisible:0,defaultConfigurationName:'Debug'});
  add(t+'Target',{isa:'PBXNativeTarget',buildConfigurationList:ref(t+'Configs'),buildPhases:phases,buildRules:[],dependencies,name:t,productName:t,productReference:ref(t+'Product'),productType:'com.apple.product-type.'+productType[targets.indexOf(t)]});
}
for(const cfg of ['Debug','Release']) add('Project'+cfg,{isa:'XCBuildConfiguration',name:cfg,baseConfigurationReference:ref('Config/Signing.xcconfig'),buildSettings:{SDKROOT:'iphoneos',IPHONEOS_DEPLOYMENT_TARGET:'16.0',CLANG_ENABLE_MODULES:'YES',CLANG_ENABLE_OBJC_ARC:'YES',CLANG_WARN_DOCUMENTATION_COMMENTS:'YES',ENABLE_USER_SCRIPT_SANDBOXING:'NO',GCC_C_LANGUAGE_STANDARD:'gnu11',SWIFT_OPTIMIZATION_LEVEL:cfg==='Debug'?'-Onone':'-O',ENABLE_TESTABILITY:cfg==='Debug'?'YES':'NO',DEBUG_INFORMATION_FORMAT:cfg==='Debug'?'dwarf':'dwarf-with-dsym',SWIFT_ACTIVE_COMPILATION_CONDITIONS:cfg==='Debug'?'DEBUG':''}});
add('ProjectConfigs',{isa:'XCConfigurationList',buildConfigurations:[ref('ProjectDebug'),ref('ProjectRelease')],defaultConfigurationIsVisible:0,defaultConfigurationName:'Debug'});
add('Products',{isa:'PBXGroup',name:'Products',children:targets.map(t=>ref(t+'Product')),sourceTree:'<group>'});
add('Main',{isa:'PBXGroup',children:[...files.map(ref),ref('NyxveilShared/CoreShim.h'),ref('NyxveilShared/NyxveilShared.h'),ref('Config/Signing.xcconfig'),ref('nvp'),ref('Products')],sourceTree:'<group>'});
add('Project',{isa:'PBXProject',attributes:{BuildIndependentTargetsInParallel:'YES',LastUpgradeCheck:'1600'},buildConfigurationList:ref('ProjectConfigs'),compatibilityVersion:'Xcode 14.0',developmentRegion:'en',hasScannedForEncodings:0,knownRegions:['en','Base'],mainGroup:ref('Main'),productRefGroup:ref('Products'),projectDirPath:'',projectRoot:'',targets:targets.map(t=>ref(t+'Target'))});
function encode(v) {
 if(v&&typeof v==='object'&&!Array.isArray(v)&&v.ref) return v.ref;
 if(Array.isArray(v)) return '('+v.map(encode).join(', ')+(v.length?',':'')+')';
 if(v&&typeof v==='object') return '{\n'+Object.entries(v).map(([k,x])=>`${/^[A-Za-z0-9_]+$/.test(k)?k:JSON.stringify(k)} = ${encode(x)};`).join('\n')+'\n}';
 return typeof v==='number'?String(v):JSON.stringify(v);
}
const projectDir=path.join(root,'Nyxveil.xcodeproj'); fs.mkdirSync(projectDir,{recursive:true});
fs.writeFileSync(path.join(projectDir,'project.pbxproj'),'// !$*UTF8*$!\n'+encode({archiveVersion:1,classes:{},objectVersion:56,objects,rootObject:ref('Project')})+'\n');
const buildable=(t)=>`<BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="${id(t+'Target')}" BuildableName="${t}.${productExt[targets.indexOf(t)]}" BlueprintName="${t}" ReferencedContainer="container:Nyxveil.xcodeproj"/>`;
const scheme=`<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion="1600" version="1.3">
<BuildAction parallelizeBuildables="YES" buildImplicitDependencies="YES">
<PreActions><ExecutionAction ActionType="Xcode.IDEStandardExecutionActionsCore.ExecutionActionType.ShellScriptAction"><ActionContent title="Build Frozen Core binding" scriptText="set -e&#10;/bin/bash &quot;$SRCROOT/scripts/build-core.sh&quot;"><EnvironmentBuildable>${buildable('Nyxveil')}</EnvironmentBuildable></ActionContent></ExecutionAction></PreActions>
<BuildActionEntries><BuildActionEntry buildForTesting="YES" buildForRunning="YES" buildForProfiling="YES" buildForArchiving="YES" buildForAnalyzing="YES">${buildable('Nyxveil')}</BuildActionEntry></BuildActionEntries></BuildAction>
<TestAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.IDEFoundation.Launcher.LLDB" shouldUseLaunchSchemeArgsEnv="YES"><Testables><TestableReference skipped="NO">${buildable('NyxveilTests')}</TestableReference></Testables></TestAction>
<LaunchAction buildConfiguration="Debug" selectedDebuggerIdentifier="Xcode.DebuggerFoundation.Debugger.LLDB" selectedLauncherIdentifier="Xcode.IDEFoundation.Launcher.LLDB" launchStyle="0" useCustomWorkingDirectory="NO" ignoresPersistentStateOnLaunch="NO" debugDocumentVersioning="YES" debugServiceExtension="internal" allowLocationSimulation="YES"><BuildableProductRunnable runnableDebuggingMode="0">${buildable('Nyxveil')}</BuildableProductRunnable></LaunchAction>
<ProfileAction buildConfiguration="Release" shouldUseLaunchSchemeArgsEnv="YES" savedToolIdentifier="" useCustomWorkingDirectory="NO" debugDocumentVersioning="YES"><BuildableProductRunnable runnableDebuggingMode="0">${buildable('Nyxveil')}</BuildableProductRunnable></ProfileAction>
<AnalyzeAction buildConfiguration="Debug"/><ArchiveAction buildConfiguration="Release" revealArchiveInOrganizer="YES"/>
</Scheme>`;
fs.mkdirSync(path.join(projectDir,'xcshareddata/xcschemes'),{recursive:true});
fs.writeFileSync(path.join(projectDir,'xcshareddata/xcschemes/Nyxveil.xcscheme'),scheme+'\n');
const plist=(body)=>`<?xml version="1.0" encoding="UTF-8"?>\n<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n<plist version="1.0"><dict>${body}</dict></plist>\n`;
for(const t of ['Nyxveil','NyxveilTunnel']) {
 fs.writeFileSync(path.join(root,t,'Info.plist'),plist(`<key>CFBundleDisplayName</key><string>${t}</string><key>CFBundleShortVersionString</key><string>0.1.0</string><key>CFBundleVersion</key><string>1</string><key>KeychainAccessGroup</key><string>$(KEYCHAIN_GROUP_IDENTIFIER)</string><key>AppGroupIdentifier</key><string>$(APP_GROUP_IDENTIFIER)</string><key>TunnelBundleIdentifier</key><string>$(TUNNEL_BUNDLE_IDENTIFIER)</string>`+(t==='NyxveilTunnel'?'<key>NSExtension</key><dict><key>NSExtensionPointIdentifier</key><string>com.apple.networkextension.packet-tunnel</string><key>NSExtensionPrincipalClass</key><string>$(PRODUCT_MODULE_NAME).PacketTunnelProvider</string></dict>':'')));
 fs.writeFileSync(path.join(root,t,t+'.entitlements'),plist('<key>com.apple.developer.networking.networkextension</key><array><string>packet-tunnel-provider</string></array><key>com.apple.security.application-groups</key><array><string>$(APP_GROUP_IDENTIFIER)</string></array><key>keychain-access-groups</key><array><string>$(KEYCHAIN_GROUP_IDENTIFIER)</string></array>'));
}
// Fail generation if a reference or source file is missing.
const walk=v=>{if(v&&typeof v==='object'){if(v.ref&&!objects[v.ref])throw Error('Missing ref '+v.ref);Object.values(v).forEach(walk);}};
Object.values(objects).forEach(walk);
console.log(`Generated and validated ${Object.keys(objects).length} Xcode objects.`);
