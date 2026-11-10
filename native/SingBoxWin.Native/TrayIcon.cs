using System.ComponentModel;
using System.Runtime.InteropServices;
namespace SingBoxWin.Native;
internal sealed class TrayIcon : IDisposable
{
    const uint Callback = 0x802a;
    readonly IntPtr hwnd, icon;
    readonly SubclassProc proc;
    readonly Action show, toggle, proxy, quit;
    readonly uint taskbarCreated;
    bool running, systemProxy, english, disposed;
    string tooltip = "sing-box for Windows";
    public bool Available { get; private set; }
    public TrayIcon(IntPtr window, string iconPath, Action show, Action toggle, Action proxy, Action quit)
    {
        hwnd = window; this.show = show; this.toggle = toggle; this.proxy = proxy; this.quit = quit;
        proc = WindowProc;
        icon = LoadImage(IntPtr.Zero, iconPath, 1, 0, 0, 0x10 | 0x40);
        if (icon == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error(), "Cannot load tray icon");
        taskbarCreated = RegisterWindowMessage("TaskbarCreated");
        if (!SetWindowSubclass(hwnd, proc, 1, 0)) { DestroyIcon(icon); throw new Win32Exception("Cannot create tray callback"); }
        Add();
    }
    NotifyData Data() => new() { Size = (uint)Marshal.SizeOf<NotifyData>(), Window = hwnd, ID = 1,
        Flags = 7, CallbackMessage = Callback, Icon = icon, Tip = tooltip, Info = "", InfoTitle = "" };
    void Add() { var data = Data(); Available = Shell_NotifyIcon(0, ref data); }
    public void Update(bool isRunning, bool hasProxy, bool isEnglish)
    {
        running = isRunning; systemProxy = hasProxy; english = isEnglish;
        string next = "sing-box for Windows · " + (running ? (systemProxy ? "Connected · System proxy" : "Running · Proxy off") : "Stopped");
        if (tooltip == next && Available) return;
        tooltip = next;
        if (!Available) Add();
        else { var data = Data(); Available = Shell_NotifyIcon(1, ref data); }
    }
    IntPtr WindowProc(IntPtr window, uint message, UIntPtr wp, IntPtr lp, UIntPtr id, UIntPtr data)
    {
        if (message == taskbarCreated) { Add(); return IntPtr.Zero; }
        if (message == Callback) {
            uint mouse = unchecked((uint)lp.ToInt64()) & 0xffff;
            if (mouse is 0x202 or 0x203 or 0x400) show();
            else if (mouse is 0x205 or 0x7b) Menu();
            return IntPtr.Zero;
        }
        return DefSubclassProc(window, message, wp, lp);
    }
    void Menu()
    {
        IntPtr menu = CreatePopupMenu();
        if (menu == IntPtr.Zero) return;
        try {
            AppendMenu(menu, 0, 1, english ? "Open sing-box for Windows" : "打开 sing-box for Windows");
            AppendMenu(menu, 0x800, 0, "");
            AppendMenu(menu, 0, 2, english ? (running ? "Stop service" : "Start service") : (running ? "停止服务" : "启动服务"));
            AppendMenu(menu, (uint)((systemProxy ? 8 : 0) | (!running ? 1 : 0)), 3, english ? "System proxy" : "系统代理");
            AppendMenu(menu, 0x800, 0, "");
            AppendMenu(menu, 0, 4, english ? "Quit (stop service and restore proxy)" : "退出（停止服务并恢复代理）");
            GetCursorPos(out var point); SetForegroundWindow(hwnd);
            uint command = TrackPopupMenu(menu, 0x102, point.X, point.Y, 0, hwnd, IntPtr.Zero);
            PostMessage(hwnd, 0, UIntPtr.Zero, IntPtr.Zero);
            switch (command) { case 1: show(); break; case 2: toggle(); break; case 3: proxy(); break; case 4: quit(); break; }
        } finally { DestroyMenu(menu); }
    }
    public void Dispose()
    {
        if (disposed) return; disposed = true;
        var data = Data(); Shell_NotifyIcon(2, ref data); Available = false;
        RemoveWindowSubclass(hwnd, proc, 1); DestroyIcon(icon); GC.KeepAlive(proc);
    }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct NotifyData {
        public uint Size; public IntPtr Window; public uint ID, Flags, CallbackMessage; public IntPtr Icon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string Tip;
        public uint State, StateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)] public string Info;
        public uint Version;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)] public string InfoTitle;
        public uint InfoFlags; public Guid Guid; public IntPtr BalloonIcon;
    }
    [StructLayout(LayoutKind.Sequential)] struct Point { public int X, Y; }
    delegate IntPtr SubclassProc(IntPtr hwnd, uint msg, UIntPtr wp, IntPtr lp, UIntPtr id, UIntPtr data);
    [DllImport("comctl32.dll")] [return: MarshalAs(UnmanagedType.Bool)] static extern bool SetWindowSubclass(IntPtr hwnd, SubclassProc proc, nuint id, nuint data);
    [DllImport("comctl32.dll")] [return: MarshalAs(UnmanagedType.Bool)] static extern bool RemoveWindowSubclass(IntPtr hwnd, SubclassProc proc, nuint id);
    [DllImport("comctl32.dll")] static extern IntPtr DefSubclassProc(IntPtr hwnd, uint msg, UIntPtr wp, IntPtr lp);
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)] [return: MarshalAs(UnmanagedType.Bool)] static extern bool Shell_NotifyIcon(uint message, ref NotifyData data);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)] static extern IntPtr LoadImage(IntPtr instance, string name, uint type, int cx, int cy, uint flags);
    [DllImport("user32.dll")] static extern bool DestroyIcon(IntPtr icon);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern uint RegisterWindowMessage(string name);
    [DllImport("user32.dll")] static extern IntPtr CreatePopupMenu();
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern bool AppendMenu(IntPtr menu, uint flags, nuint id, string text);
    [DllImport("user32.dll")] static extern uint TrackPopupMenu(IntPtr menu, uint flags, int x, int y, int reserved, IntPtr window, IntPtr rect);
    [DllImport("user32.dll")] static extern bool DestroyMenu(IntPtr menu);
    [DllImport("user32.dll")] static extern bool GetCursorPos(out Point point);
    [DllImport("user32.dll")] internal static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] static extern bool PostMessage(IntPtr window, uint msg, UIntPtr wp, IntPtr lp);
}
