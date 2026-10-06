import CryptoKit
import Foundation
import Security

// Probe: can an ad-hoc signed CLI create Secure Enclave keys (no keychain
// entitlement), with and without a user-presence access control?
print("SecureEnclave.isAvailable =", SecureEnclave.isAvailable)

do {
    let plain = try SecureEnclave.P256.Signing.PrivateKey()
    let sig = try plain.signature(for: Data("challenge".utf8))
    let ok = plain.publicKey.isValidSignature(sig, for: Data("challenge".utf8))
    print("no-ACL key: created, blob", plain.dataRepresentation.count, "bytes, sign+verify", ok)
    let reloaded = try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: plain.dataRepresentation)
    print("no-ACL key: reloaded from blob, same pubkey", reloaded.publicKey.rawRepresentation == plain.publicKey.rawRepresentation)
} catch {
    print("no-ACL key failed:", error)
}

var cfErr: Unmanaged<CFError>?
if let acl = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly, [.privateKeyUsage, .userPresence], &cfErr) {
    do {
        let k = try SecureEnclave.P256.Signing.PrivateKey(accessControl: acl)
        print("userPresence key: created (no prompt at creation), blob", k.dataRepresentation.count, "bytes")
    } catch {
        print("userPresence key failed:", error)
    }
} else {
    print("ACL create failed:", cfErr!.takeRetainedValue())
}
