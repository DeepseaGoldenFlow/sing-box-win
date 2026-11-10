using Microsoft.UI.Xaml;
using System.Runtime.InteropServices;
namespace SingBoxWin.Native;
public partial class App : Application
{
    private Mutex? instance;
    private EventWaitHandle? activation;
    private MainWindow? window;
    public App() { InitializeComponent(); }
    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        instance = new Mutex(true, @"Global\SingBoxWinNative", out bool first);
        activation = new EventWaitHandle(false, EventResetMode.AutoReset, @"Global\SingBoxWinNativeActivate");
        if (!first) { activation.Set(); Exit(); return; }
        window = new MainWindow(); window.Activate();
        _ = Task.Run(() => { while (activation.WaitOne()) window.DispatcherQueue.TryEnqueue(() => {
            window.AppWindow.Show();
            if (window.AppWindow.Presenter is Microsoft.UI.Windowing.OverlappedPresenter presenter) presenter.Restore();
            window.Activate(); SetForegroundWindow(WinRT.Interop.WindowNative.GetWindowHandle(window));
        }); });
    }
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr hWnd);
}
