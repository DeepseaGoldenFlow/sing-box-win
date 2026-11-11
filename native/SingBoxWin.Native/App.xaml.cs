using Microsoft.UI.Xaml;
using System.Diagnostics;
using System.Runtime.InteropServices;
namespace SingBoxWin.Native;
public partial class App : Application
{
    // Earliest managed timestamp. The engine reports its own t0 (process start)
    // and startup.go measures every other stage against it; the window reports
    // its first painted frame relative to this clock so requirement C has a
    // number instead of an impression. Activity is measured in the client, the
    // engine only records it.
    internal static readonly Stopwatch StartupClock = Stopwatch.StartNew();
    private MainWindow? window;
    private static void WriteError(object? error)
    {
        try {
            System.IO.Directory.CreateDirectory(EngineClient.DataDir);
            System.IO.File.AppendAllText(System.IO.Path.Combine(EngineClient.DataDir, "native-error.log"), $"{DateTimeOffset.Now:O} {error}{Environment.NewLine}");
        } catch { }
    }
    public App()
    {
        UnhandledException += (_, e) => WriteError(e.Exception);
        AppDomain.CurrentDomain.UnhandledException += (_, e) => WriteError(e.ExceptionObject);
        InitializeComponent();
    }
    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        // A second launch only brings the existing window forward. It does not
        // start an engine and does not import a deep link; profiles and links
        // still require the engine's authenticated activation path.
        if (!NativeActivation.Acquire()) { NativeActivation.SignalPrimary(); Exit(); return; }
        window = new MainWindow(); window.Activate();
        NativeActivation.Listen(() => window.DispatcherQueue.TryEnqueue(() => {
            window.AppWindow.Show();
            if (window.AppWindow.Presenter is Microsoft.UI.Windowing.OverlappedPresenter presenter) presenter.Restore();
            window.Activate(); SetForegroundWindow(WinRT.Interop.WindowNative.GetWindowHandle(window));
        }));
    }
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr hWnd);
}
