// Security.framework owns the access decision. Never downgrade to login-keychain reads.
import Foundation
import Security
import LocalAuthentication
import Darwin

let service = "org.simple-budget.server-admin.v1"
func emit(_ value: [String:Any], to handle: FileHandle) {
    guard let bytes = try? JSONSerialization.data(withJSONObject:value) else { exit(2) }
    handle.write(bytes); handle.write(Data([10]))
}
func capabilities() -> [String:Any] {
    let ctx=LAContext(); ctx.interactionNotAllowed=true; var error:NSError?
    let available=ctx.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics,error:&error)
    // A synthetic non-secret item proves Data Protection Keychain access without prompting.
    let query:[String:Any] = [kSecClass as String:kSecClassGenericPassword,
        kSecAttrService as String:service+".probe", kSecAttrAccount as String:UUID().uuidString,
        kSecUseDataProtectionKeychain as String:true, kSecValueData as String:Data("synthetic-probe".utf8),
        kSecUseAuthenticationContext as String:ctx]
    let status=SecItemAdd(query as CFDictionary,nil)
    if status == errSecSuccess { var q=query; q.removeValue(forKey:kSecValueData as String); SecItemDelete(q as CFDictionary) }
    return ["biometrics_available":available,"touch_id":ctx.biometryType == .touchID,
        "la_error":error?.code ?? 0,"data_protection_status":Int(status),
        "storage_available":status == errSecSuccess && available,
        "policy":"biometryCurrentSet; Data Protection Keychain; no login-keychain fallback"]
}
if CommandLine.arguments == [CommandLine.arguments[0],"status"] {
    emit(capabilities(),to:.standardOutput); exit(0)
}
guard CommandLine.arguments.count == 3, CommandLine.arguments[1] == "channel",
      let fd=Int32(CommandLine.arguments[2]) else { exit(2) }
var info=stat()
guard fstat(fd,&info)==0, (info.st_mode & S_IFMT)==S_IFSOCK else { exit(2) }
alarm(900)
var receiveTimeout=timeval(tv_sec:900,tv_usec:0)
setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&receiveTimeout,socklen_t(MemoryLayout<timeval>.size))
let channel=FileHandle(fileDescriptor:fd,closeOnDealloc:true)
let context=LAContext(); context.localizedFallbackTitle=""; context.touchIDAuthenticationAllowableReuseDuration=0
var authenticated=false
var scope:String?=nil
let deadline=Date().addingTimeInterval(900)
func authenticate(_ account:String) -> Bool {
    if authenticated { return true }
    let done=DispatchSemaphore(value:0); var ok=false
    context.evaluatePolicy(.deviceOwnerAuthenticationWithBiometrics,
        localizedReason:"Одна операция Simple Budget: " + account) { success,_ in ok=success; done.signal() }
    if done.wait(timeout:.now()+120) != .success { context.invalidate(); return false }
    authenticated=ok; return ok
}
var buffer=Data(); var count=0
while Date()<deadline && count<24 {
    let byte=channel.readData(ofLength:1); if byte.isEmpty { break }
    buffer.append(byte)
    if buffer.count>16384 { break }
    if byte[0] != 10 { continue }
    count+=1
    guard let req=(try? JSONSerialization.jsonObject(with:buffer)) as? [String:String],
          let op=req["op"], ["put","get","delete"].contains(op), let account=req["account"],
          account.range(of:"^171[.]22[.]237[.]16/(acceptance|production)/[a-f0-9]{32}/(app|client|pki)/[a-z][a-z0-9-]{0,39}$",options:.regularExpression) != nil else {
        emit(["error":"invalid_request"],to:channel); break
    }
    buffer.removeAll(keepingCapacity:false)
    let prefix=account.split(separator:"/").prefix(3).joined(separator:"/")
    if scope == nil { scope=prefix }
    guard scope==prefix else { emit(["error":"scope_mismatch"],to:channel); break }
    guard authenticate(account) else { emit(["error":"authentication_cancelled_or_unavailable"],to:channel); break }
    var query:[String:Any] = [kSecClass as String:kSecClassGenericPassword,
        kSecAttrService as String:service,kSecAttrAccount as String:account,
        kSecUseDataProtectionKeychain as String:true,kSecUseAuthenticationContext as String:context]
    var status:OSStatus=errSecParam
    var response:[String:Any]=[:]
    if op == "get" {
        query[kSecReturnData as String]=true; query[kSecMatchLimit as String]=kSecMatchLimitOne
        var item:CFTypeRef?; status=SecItemCopyMatching(query as CFDictionary,&item)
        if status == errSecSuccess, let data=item as? Data, let value=String(data:data,encoding:.utf8) { response["value"]=value }
    } else if op == "delete" { status=SecItemDelete(query as CFDictionary) }
    else if let value=req["value"], !value.isEmpty, value.utf8.count<=4096,
            !value.contains("\0"), !value.contains("\n"), !value.contains("\r") {
        var error:Unmanaged<CFError>?
        if let acl=SecAccessControlCreateWithFlags(nil,kSecAttrAccessibleWhenUnlockedThisDeviceOnly,.biometryCurrentSet,&error) {
            let attrs:[String:Any]=[kSecValueData as String:Data(value.utf8),kSecAttrAccessControl as String:acl]
            var add=query; attrs.forEach { add[$0.key]=$0.value }; status=SecItemAdd(add as CFDictionary,nil)
            if status == errSecDuplicateItem { status=SecItemUpdate(query as CFDictionary,attrs as CFDictionary) }
        }
    }
    response["status"]=Int(status); emit(response,to:channel)
    if status != errSecSuccess && status != errSecItemNotFound { break }
}
context.invalidate()
