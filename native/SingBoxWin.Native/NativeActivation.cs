using System.Runtime.InteropServices;
using System.Security.Principal;
namespace SingBoxWin.Native;
// Second-instance activation (proposal §0.1 and §0.2.5).
//
// The previous Global\ mutex and event had no DACL, so any SID or session could
// create or signal them; they were not an authentication boundary. Here both
// kernel objects carry an explicit DACL limited to the current user SID (plus
// SYSTEM), the names include the SID so users cannot collide, and the payload
// is only "show the window". A deep link or profile path is never forwarded
// through this channel; those still use the engine's authenticated
// /api/activate endpoint.
public static class NativeActivation
{
    private static IntPtr mutex;
    private static IntPtr activation;
    private static Thread? listener;
    private static volatile bool stopping;

    public static bool Acquire()
    {
        var sid = WindowsIdentity.GetCurrent().User ?? throw new InvalidOperationException("No user SID.");
        var security = SecurityAttributes("D:(A;;GA;;;SY)(A;;GA;;;" + sid.Value + ")");
        // Existence of the mutex is the single-instance signal; ownership is
        // deliberately not taken, so Dispose can run on any thread.
        mutex = CreateMutexW(ref security, false, @"Local\SingBoxWinNative-" + sid.Value);
        int error = Marshal.GetLastWin32Error();
        if (mutex == IntPtr.Zero) throw new InvalidOperationException("CreateMutex failed: " + error);
        if (error == ERROR_ALREADY_EXISTS) { CloseHandle(mutex); mutex = IntPtr.Zero; return false; }
        // EVENT_MODIFY_STATE|SYNCHRONIZE for the user, nothing for anyone else.
        activation = CreateEventW(ref security, false, false, @"Local\SingBoxWinNativeActivate-" + sid.Value);
        if (activation == IntPtr.Zero) throw new InvalidOperationException("CreateEvent failed: " + Marshal.GetLastWin32Error());
        return true;
    }

    public static void SignalPrimary()
    {
        try
        {
            var sid = WindowsIdentity.GetCurrent().User;
            if (sid is null) return;
            // The primary may still be between CreateMutex and CreateEvent when a
            // second launch races it; retry briefly rather than silently losing
            // the "show the window" request.
            for (int attempt = 0; attempt < 20; attempt++)
            {
                var handle = OpenEventW(EVENT_MODIFY_STATE, false, @"Local\SingBoxWinNativeActivate-" + sid.Value);
                if (handle != IntPtr.Zero)
                {
                    SetEvent(handle);
                    CloseHandle(handle);
                    return;
                }
                Thread.Sleep(50);
            }
        }
        catch { /* the primary may be starting or shutting down */ }
    }

    // Listen runs on a background thread and calls show for each activation.
    public static void Listen(Action show)
    {
        if (activation == IntPtr.Zero) return;
        stopping = false;
        listener = new Thread(() =>
        {
            while (!stopping)
            {
                uint result = WaitForSingleObject(activation, 500);
                if (stopping) return;
                if (result == WAIT_OBJECT_0) show();
            }
        }) { IsBackground = true, Name = "SFW activation" };
        listener.Start();
    }

    public static void Dispose()
    {
        stopping = true;
        listener?.Join(1500);
        listener = null;
        if (activation != IntPtr.Zero) { CloseHandle(activation); activation = IntPtr.Zero; }
        // The mutex is never owned (see Acquire), so closing the handle is the
        // whole release; the name becomes free once the last handle closes.
        if (mutex != IntPtr.Zero) { CloseHandle(mutex); mutex = IntPtr.Zero; }
    }

    private const int EVENT_MODIFY_STATE = 0x0002;
    private const uint WAIT_OBJECT_0 = 0;
    private const int ERROR_ALREADY_EXISTS = 183;

    [StructLayout(LayoutKind.Sequential)]
    private struct SECURITY_ATTRIBUTES { public int nLength; public IntPtr lpSecurityDescriptor; public bool bInheritHandle; }

    private static SECURITY_ATTRIBUTES SecurityAttributes(string sddl)
    {
        if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl, 1, out IntPtr descriptor, IntPtr.Zero))
            throw new InvalidOperationException("Invalid activation DACL: " + Marshal.GetLastWin32Error());
        return new SECURITY_ATTRIBUTES { nLength = Marshal.SizeOf<SECURITY_ATTRIBUTES>(), lpSecurityDescriptor = descriptor, bInheritHandle = false };
    }

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool ConvertStringSecurityDescriptorToSecurityDescriptorW(string sddl, uint revision, out IntPtr descriptor, IntPtr size);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateMutexW(ref SECURITY_ATTRIBUTES attributes, bool initialOwner, string name);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateEventW(ref SECURITY_ATTRIBUTES attributes, bool manualReset, bool initialState, string name);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr OpenEventW(int desiredAccess, bool inheritHandle, string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool SetEvent(IntPtr handle);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool CloseHandle(IntPtr handle);
}
