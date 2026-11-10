using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Microsoft.UI.Windowing;
using System.Diagnostics;
using System.Text.Json.Nodes;
using Windows.Foundation;
using Windows.Graphics;
using Windows.UI;

namespace SingBoxWin.Native;
public sealed partial class MainWindow : Window
{
    private readonly EngineClient api = new();
    private readonly DispatcherTimer timer = new() { Interval = TimeSpan.FromSeconds(1) };
    private readonly Queue<(double up, double down)> samples = new();
    private ResourceDictionary strings = new();
    private string language = "zh-CN", page = "Dashboard", level = "";
    private bool connected, refreshing, acting, rendering, closeExits, exiting, autoScroll = true;
    private JsonNode? state;
    private TextBlock? status, profile, up, down, totals, tailStatus, host, ips, expiry, sync, testResult, tailHint;
    private StackPanel? profileList;
    private ListView? list;
    private Canvas? chart;
    private Button? start, stop, restart;
    private readonly string prefsPath = System.IO.Path.Combine(EngineClient.DataDir, "native-preferences.json");
    public MainWindow()
    {
        InitializeComponent();
        Title = "sing-box · Windows";
        SystemBackdrop = new MicaBackdrop();
        ExtendsContentIntoTitleBar = true; SetTitleBar(TitleBar);
        AppWindow.Resize(new SizeInt32(1040, 720));
        Root.ActualThemeChanged += (_, _) => ApplyTitleTheme();
        ApplyTitleTheme();
        AppWindow.Changed += (_, args) => {
            if (args.DidSizeChange && AppWindow.Presenter is OverlappedPresenter op && op.State != OverlappedPresenterState.Minimized) {
                var size = AppWindow.Size;
                if (size.Width < 800 || size.Height < 500) AppWindow.Resize(new SizeInt32(Math.Max(800, size.Width), Math.Max(500, size.Height)));
            }
        };
        AppWindow.Closing += (sender, args) => {
            if (exiting) return;
            args.Cancel = true;
            if (closeExits)
            {
                _ = Run(Quit);
            }
            else
            {
                AppWindow.Hide();
            }
        };
        try { closeExits = JsonNode.Parse(System.IO.File.ReadAllText(prefsPath))?["close_exits"]?.GetValue<bool>() ?? false; } catch { }
        LoadPreferences();
        LoadLanguage("zh-CN");
        Nav.SelectionChanged += (_, _) => { if (!rendering && Nav.SelectedItem is NavigationViewItem item) { page = (string)item.Tag; Render(); _ = Refresh(); } };
        timer.Tick += async (_, _) => await Refresh();
        Activated += OnFirstActivated;
    }
    private async void OnFirstActivated(object sender, WindowActivatedEventArgs args)
    {
        Activated -= OnFirstActivated;
        await Run(async () => {
            await api.ConnectAsync(); connected = true;
            var settings = await api.Call("settings");
            LoadLanguage(S(settings, "language").StartsWith("en") ? "en-US" : "zh-CN");
            Root.RequestedTheme = S(settings, "theme") switch { "light" => ElementTheme.Light, "system" => ElementTheme.Default, _ => ElementTheme.Dark };
            Render(); timer.Start(); await Refresh();
        });
        if (!connected) { var retry = Button(T("Retry"), async () => { await api.ConnectAsync(); connected = true; Render(); timer.Start(); await Refresh(); }); Body().Children.Add(retry); }
    }
    private string T(string key) => strings.ContainsKey(key) ? (string)strings[key] : key;
    private void LoadLanguage(string value)
    {
        language = value;
        strings = new ResourceDictionary { Source = new Uri($"ms-appx:///Strings/{value}.xaml") };
        rendering = true;
        Nav.MenuItems.Clear();
        string[] keys = ["Dashboard", "Groups", "Connections", "Logs", "Tools", "Settings"];
        string[] glyphs = ["\uE80F", "\uE8A5", "\uE8CE", "\uE9D9", "\uE90F", "\uE713"];
        for (int i = 0; i < keys.Length; i++) {
            var item = new NavigationViewItem { Content = T(keys[i]), Tag = keys[i], FontSize = 13, MinHeight = 36, Icon = new FontIcon { Glyph = glyphs[i], FontSize = 16, FontFamily = new FontFamily("Segoe Fluent Icons") } };
            Nav.MenuItems.Add(item); if (keys[i] == page) Nav.SelectedItem = item;
        }
        rendering = false; UpdateControls(); Render();
    }
    private static string S(JsonNode? n, string key) => n?[key]?.ToString() ?? "";
    private static bool B(JsonNode? n, string key) => n?[key]?.GetValue<bool>() ?? false;
    private static double N(JsonNode? n, string key) => n?[key]?.GetValue<double>() ?? 0;
    private static SolidColorBrush Brush(string color) => new(Color.FromArgb(255, Convert.ToByte(color.Substring(1, 2), 16), Convert.ToByte(color.Substring(3, 2), 16), Convert.ToByte(color.Substring(5, 2), 16)));
    private static string Bytes(double value) { string[] units = ["B", "KB", "MB", "GB", "TB"]; int unit = 0; while (value >= 1024 && unit < 4) { value /= 1024; unit++; } return $"{value:0.#} {units[unit]}"; }
    private static string Date(string value) => DateTimeOffset.TryParse(value, out var dt) ? dt.ToLocalTime().ToString("yyyy-MM-dd HH:mm:ss") : "—";
    private TextBlock Text(string value, double size = 13, string? color = null)
    {
        var t = new TextBlock { Text = value, FontSize = size, TextWrapping = TextWrapping.Wrap, FontFamily = new FontFamily("Segoe UI Variable Text, Microsoft YaHei UI") };
        if (color is not null) t.Foreground = Brush(color); return t;
    }
    private static void Mono(TextBlock text) => text.FontFamily = new FontFamily("Cascadia Mono, Consolas");
    private void ApplyTitleTheme()
    {
        bool light = Root.ActualTheme == ElementTheme.Light;
        var bar = AppWindow.TitleBar;
        bar.ButtonBackgroundColor = bar.ButtonInactiveBackgroundColor = Colors.Transparent;
        bar.ButtonForegroundColor = light ? Colors.Black : Colors.White;
        bar.ButtonInactiveForegroundColor = light ? Colors.DimGray : Colors.LightGray;
        bar.ButtonHoverBackgroundColor = Color.FromArgb(26, light ? (byte)0 : (byte)255, light ? (byte)0 : (byte)255, light ? (byte)0 : (byte)255);
        bar.ButtonHoverForegroundColor = bar.ButtonForegroundColor;
    }
    private static void Enable(Button button, bool enabled)
    {
        button.IsEnabled = enabled;
        button.Opacity = enabled ? 1 : .4;
    }
    private StackPanel Row(params UIElement[] elements) { var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 12 }; foreach (var element in elements) row.Children.Add(element); return row; }
    private Border Card(UIElement child) { var c = new Border { Child = child, Padding = new Thickness(20), CornerRadius = new CornerRadius(12), BorderThickness = new Thickness(1) }; var resources = (ResourceDictionary)Application.Current.Resources.ThemeDictionaries[Root.RequestedTheme == ElementTheme.Light ? "Light" : "Dark"]; c.Background = (Microsoft.UI.Xaml.Media.Brush)resources["CardBrush"]; c.BorderBrush = (Microsoft.UI.Xaml.Media.Brush)resources["EdgeBrush"]; return c; }
    private Button Button(string title, Func<Task> action, bool primary = false)
    {
        var button = new Button { Content = title };
        if (primary) button.Style = (Style)Application.Current.Resources["AccentButtonStyle"];
        button.Resources["AccentButtonBackgroundDisabled"] = Brush(Root.ActualTheme == ElementTheme.Light ? "#E5E5EA" : "#3A3A3C");
        button.Click += async (_, _) => { if (acting) return; acting = true; Enable(button, false); await Run(action); acting = false; Enable(button, true); UpdateControls(); await Refresh(); };
        return button;
    }
    private async Task Run(Func<Task> action)
    {
        Busy.IsActive = true;
        try { await action(); Notice.IsOpen = false; }
        catch (Exception ex) { Notice.Message = ex.Message; Notice.IsOpen = true; }
        finally { Busy.IsActive = false; }
    }
    private StackPanel Body()
    {
        if (ContentFrame.Content is ScrollViewer scroll && scroll.Content is StackPanel panel) return panel;
        var body = new StackPanel { Spacing = 16 };
        ContentFrame.Content = new ScrollViewer { Content = body, HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled };
        return body;
    }
    private void Render()
    {
        if (rendering) return;
        ContentFrame.Content = null; Heading.Text = T(page);
        Toolbar.Children.Clear();
        if (page == "Dashboard") Toolbar.Children.Add(Button(T("Dashboard Items"), ManageCards));
        toolbarStart = Button(T("Start"), () => Core(Session.IsConnected ? "stop" : "start"), true); Toolbar.Children.Add(toolbarStart); UpdateControls();
        status = profile = up = down = totals = tailStatus = host = ips = expiry = sync = testResult = tailHint = null;
        profileList = null; list = null; chart = null;
        var body = Body();
        switch (page) {
            case "Dashboard": RenderDashboard(body); break;
            case "Groups": RenderGroups(body); break;
            case "Connections": RenderConnections(body); break;
            case "Logs": RenderLogs(body); break;
            case "Tools":
                var tail = new StackPanel { Spacing = 16 }; tail.Children.Add(Text("Tailscale", 13, "#9A9AA0")); tailStatus = Text(T("Disabled"), 18, "#8E8E93"); tail.Children.Add(tailStatus); tailHint = Text(T("NoTail"), 13, "#9A9AA0"); tail.Children.Add(tailHint);
                host = Text("—"); ips = Text("—"); expiry = Text("—"); sync = Text("—");
                foreach (var pair in new[] { ("Hostname", host), ("TailnetIP", ips), ("KeyExpiry", expiry), ("LastSync", sync) }) { var field = new StackPanel { Spacing = 4 }; field.Children.Add(Text(T(pair.Item1), 12, "#8E8E93")); field.Children.Add(pair.Item2); tail.Children.Add(field); }
                tail.Children.Add(Text(T("HandshakeHint"), 12, "#9A9AA0")); tail.Children.Add(Button(T("Test"), TestTail, true)); testResult = Text(""); tail.Children.Add(testResult); body.Children.Add(Card(tail));
                foreach (string name in new[] { "Tailscale SSH", "USBIP", "OpenConnect", "OpenVPN" }) { var row = new Grid { Opacity = .45 }; row.ColumnDefinitions.Add(new ColumnDefinition()); row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto }); row.Children.Add(Text(name)); var hint = Text(T("Unsupported"), 12, "#8E8E93"); Grid.SetColumn(hint, 1); row.Children.Add(hint); body.Children.Add(Card(row)); } break;
            case "Settings": _ = Run(RenderSettings); break;
        }
    }
    private async Task Core(string action) {
        transition = action == "stop" ? ConnectionPhase.Disconnecting : action == "restart" ? ConnectionPhase.Reasserting : ConnectionPhase.Connecting;
        UpdateControls();
        try { await api.Call("core/" + action, HttpMethod.Post); }
        finally { transition = null; state = await api.Call("state"); UpdateControls(); Render(); }
    }
    private async Task Import()
    {
        var fields = new StackPanel { Spacing = 12 }; var name = new TextBox { Header = T("Name") }; var url = new TextBox { Header = T("URL"), PlaceholderText = "https://" }; fields.Children.Add(name); fields.Children.Add(url);
        var dialog = new ContentDialog { XamlRoot = Root.XamlRoot, RequestedTheme = Root.ActualTheme, Title = T("Import"), Content = fields, PrimaryButtonText = T("Import"), CloseButtonText = T("Cancel"), DefaultButton = ContentDialogButton.Primary };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary) return;
        if (!Uri.TryCreate(url.Text, UriKind.Absolute, out var uri) || (uri.Scheme != "https" && uri.Scheme != "http")) throw new InvalidOperationException(T("InvalidURL"));
        await api.Call("profiles/import", HttpMethod.Post, new { name = name.Text, url = url.Text });
    }
    private async Task TestTail()
    {
        JsonNode? result;
        try { result = await api.Call("tools/tailscale/test", HttpMethod.Post); }
        catch (Exception ex) { if (testResult is not null) { testResult.Text = ex.Message; testResult.Foreground = Brush("#FF453A"); } return; }
        if (testResult is null) return;
        testResult.Text = $"HTTP {N(result, "status"):0} · {N(result, "ms"):0} ms" + (B(result, "ok") ? "" : " · " + S(result, "error"));
        testResult.Foreground = Brush(B(result, "ok") ? "#30D158" : "#FF453A");
    }
    private async Task Quit() { if (connected) await api.Call("quit", HttpMethod.Post); exiting = true; Application.Current.Exit(); }
    private async Task Refresh()
    {
        if (!connected || refreshing || acting) return;
        refreshing = true;
        string requestedPage = page;
        try {
            state = await api.Call("state");
            UpdateControls();
            if (page != requestedPage) return;
            switch (requestedPage) {
                case "Dashboard": await RefreshDashboard(); break;
                case "Groups": await RefreshGroups(); break;
                case "Tools":
                    var tail = await api.Call("tools/tailscale", missingAllowed: true); if (page != requestedPage || tailStatus is null) break;
                    tailHint!.Visibility = tail is null || !B(tail, "enabled") || new[] { "hostname", "key_expiry", "last_sync" }.Any(key => S(tail, key) == "") || tail["ips"] is not JsonArray addresses || addresses.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
                    tailStatus.Text = tail?["enabled"] is null ? "—" : T(!B(tail, "enabled") ? "Disabled" : B(tail, "registered") ? "Registered" : "Unregistered"); tailStatus.Foreground = Brush(B(tail, "enabled") && B(tail, "registered") ? "#30D158" : "#8E8E93");
                    host!.Text = S(tail, "hostname") is "" ? "—" : S(tail, "hostname"); ips!.Text = tail?["ips"] is JsonArray ipArray && ipArray.Count > 0 ? string.Join("\n", ipArray.Select(x => x?.ToString())) : "—"; expiry!.Text = Date(S(tail, "key_expiry")); sync!.Text = "—"; // The API exposes cache sync time, not a handshake timestamp.
                    break;
                case "Connections": await RefreshConnections(); break;
                case "Logs": _ = Run(RefreshLogs); break;
            }
        } catch (Exception ex) { Notice.Message = ex.Message; Notice.IsOpen = true; }
        finally { refreshing = false; }
    }
    private void DrawChart()
    {
        if (chart is null || chart.ActualWidth <= 0) return;
        chart.Children.Clear(); double width = chart.ActualWidth, height = chart.Height;
        for (int i = 0; i < 4; i++) chart.Children.Add(new Line { X1 = 0, X2 = width, Y1 = height * i / 3, Y2 = height * i / 3, Stroke = new SolidColorBrush(Color.FromArgb(34, Root.ActualTheme == ElementTheme.Light ? (byte)0 : (byte)255, Root.ActualTheme == ElementTheme.Light ? (byte)0 : (byte)255, Root.ActualTheme == ElementTheme.Light ? (byte)0 : (byte)255)), StrokeThickness = 1 });
        if (!samples.Any(v => v.up > 0 || v.down > 0)) {
            var hint = Text(T("WaitingTraffic"), 13, "#9A9AA0");
            hint.Measure(new Size(width, height)); Canvas.SetLeft(hint, Math.Max(0, (width - hint.DesiredSize.Width) / 2)); Canvas.SetTop(hint, (height - hint.DesiredSize.Height) / 2); chart.Children.Add(hint); return;
        }
        var values = Enumerable.Repeat((up: 0d, down: 0d), 60 - samples.Count).Concat(samples).ToArray();
        double max = Math.Max(1024, values.Max(v => Math.Max(v.up, v.down)) * 1.15);
        var ul = new Polyline { Stroke = Brush("#FF9F0A"), StrokeThickness = 2 }; var dl = new Polyline { Stroke = Brush("#30D158"), StrokeThickness = 2 };
        for (int i = 0; i < values.Length; i++) { ul.Points.Add(new Point(width * i / 59, height - values[i].up / max * (height - 4))); dl.Points.Add(new Point(width * i / 59, height - values[i].down / max * (height - 4))); }
        foreach (var line in new[] { ul, dl }) {
            var area = new Polygon { Fill = new LinearGradientBrush { StartPoint = new Point(0, 0), EndPoint = new Point(0, 1), GradientStops = { new GradientStop { Color = Color.FromArgb(77, 10, 132, 255), Offset = 0 }, new GradientStop { Color = Color.FromArgb(0, 10, 132, 255), Offset = 1 } } } };
            area.Points.Add(new Point(0, height)); foreach (var point in line.Points) area.Points.Add(point); area.Points.Add(new Point(width, height)); chart.Children.Add(area);
        }
        chart.Children.Add(ul); chart.Children.Add(dl);
    }
}
