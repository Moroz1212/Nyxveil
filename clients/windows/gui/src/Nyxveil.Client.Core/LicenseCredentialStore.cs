using System.Runtime.InteropServices;
using System.Text;

namespace Nyxveil.Client.Core;

/// <summary>Windows Credential Manager storage for License Credential (never LocalSystem).</summary>
public static class LicenseCredentialStore
{
    private const string Target = "Nyxveil/LicenseCredential";

    public static void Save(string licenseToken)
    {
        var bytes = Encoding.UTF8.GetBytes(licenseToken);
        var blob = new CREDENTIAL
        {
            Type = CRED_TYPE_GENERIC,
            TargetName = Target,
            CredentialBlobSize = (uint)bytes.Length,
            CredentialBlob = Marshal.AllocHGlobal(bytes.Length),
            Persist = CRED_PERSIST_LOCAL_MACHINE,
            UserName = Environment.UserName
        };
        try
        {
            Marshal.Copy(bytes, 0, blob.CredentialBlob, bytes.Length);
            if (!CredWrite(ref blob, 0))
                throw new InvalidOperationException("CredWrite failed: " + Marshal.GetLastWin32Error());
        }
        finally
        {
            Marshal.FreeHGlobal(blob.CredentialBlob);
        }
    }

    public static string? Load()
    {
        if (!CredRead(Target, CRED_TYPE_GENERIC, 0, out var ptr))
            return null;
        try
        {
            var cred = Marshal.PtrToStructure<CREDENTIAL>(ptr);
            if (cred.CredentialBlob == IntPtr.Zero || cred.CredentialBlobSize == 0)
                return null;
            var bytes = new byte[cred.CredentialBlobSize];
            Marshal.Copy(cred.CredentialBlob, bytes, 0, bytes.Length);
            return Encoding.UTF8.GetString(bytes);
        }
        finally
        {
            CredFree(ptr);
        }
    }

    public static bool Exists() => !string.IsNullOrWhiteSpace(Load());

    public static void Delete()
    {
        CredDelete(Target, CRED_TYPE_GENERIC, 0);
    }

    private const int CRED_TYPE_GENERIC = 1;
    /// <summary>Persist for this user on this machine (Credential Manager — not LocalSystem).</summary>
    private const int CRED_PERSIST_LOCAL_MACHINE = 2;

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct CREDENTIAL
    {
        public uint Flags;
        public int Type;
        public string TargetName;
        public string Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public uint CredentialBlobSize;
        public IntPtr CredentialBlob;
        public uint Persist;
        public uint AttributeCount;
        public IntPtr Attributes;
        public string TargetAlias;
        public string UserName;
    }

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool CredWrite([In] ref CREDENTIAL userCredential, uint flags);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool CredRead(string target, int type, int reservedFlag, out IntPtr credentialPtr);

    [DllImport("advapi32.dll", SetLastError = true)]
    private static extern bool CredFree(IntPtr buffer);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool CredDelete(string target, int type, int flags);
}
