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
                if (size.Width < 900 || size.Height < 620) AppWindow.Resize(new SizeInt32(Math.Max(900, size.Width), Math.Max(620, size.Height)));
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
            Root.RequestedTheme = S(settings, "theme") == "light" ? ElementTheme.Light : ElementTheme.Dark;
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
        string[] keys = ["Dashboard", "Profiles", "Connections", "Logs", "Tools", "Settings"];
        string[] glyphs = ["\uE80F", "\uE8A5", "\uE8CE", "\uE9D9", "\uE90F", "\uE713"];
        for (int i = 0; i < keys.Length; i++) {
            var item = new NavigationViewItem { Content = T(keys[i]), Tag = keys[i], FontSize = 13, MinHeight = 36, Icon = new FontIcon { Glyph = glyphs[i], FontSize = 16, FontFamily = new FontFamily("Segoe Fluent Icons") } };
            Nav.MenuItems.Add(item); if (keys[i] == page) Nav.SelectedItem = item;
        }
        rendering = false; Render();
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
        button.Click += async (_, _) => { if (acting) return; acting = true; Enable(button, false); await Run(action); acting = false; Enable(button, true); await Refresh(); };
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
        status = profile = up = down = totals = tailStatus = host = ips = expiry = sync = testResult = tailHint = null;
        profileList = null; list = null; chart = null;
        var body = Body();
        switch (page) {
            case "Dashboard":
                status = Text(T("Stopped"), 28, "#8E8E93"); status.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
                profile = Text(T("NoProfile"), 13, "#8E8E93");
                start = Button(T("Start"), () => Core("start"), true); stop = Button(T("Stop"), () => Core("stop")); restart = Button(T("Restart"), () => Core("restart"));
                Enable(start, false); Enable(stop, false); Enable(restart, false);
                stop.Background = Brush(Root.ActualTheme == ElementTheme.Light ? "#E5E5EA" : "#3A3A3C");
                var stateBox = new StackPanel { Spacing = 12 }; stateBox.Children.Add(status); stateBox.Children.Add(profile); stateBox.Children.Add(Row(start, stop, restart)); body.Children.Add(Card(stateBox));
                up = Text("0 B/s", 28, "#FF9F0A"); down = Text("0 B/s", 28, "#30D158"); Mono(up); Mono(down); up.FontWeight = down.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold;
                var metrics = new Grid { ColumnSpacing = 16 }; metrics.ColumnDefinitions.Add(new ColumnDefinition()); metrics.ColumnDefinitions.Add(new ColumnDefinition());
                var u = new StackPanel { Spacing = 12 }; u.Children.Add(Text("↑ " + T("Upload"), 13, "#9A9AA0")); u.Children.Add(up);
                var d = new StackPanel { Spacing = 12 }; d.Children.Add(Text("↓ " + T("Download"), 13, "#9A9AA0")); d.Children.Add(down);
                metrics.Children.Add(Card(u)); var dc = Card(d); Grid.SetColumn(dc, 1); metrics.Children.Add(dc); body.Children.Add(metrics);
                var plot = new StackPanel { Spacing = 16 }; var legend = Row(Text(T("Traffic")), Text("— " + T("Upload"), 12, "#FF9F0A"), Text("— " + T("Download"), 12, "#30D158")); legend.HorizontalAlignment = HorizontalAlignment.Right; plot.Children.Add(legend);
                chart = new Canvas { Height = 150, HorizontalAlignment = HorizontalAlignment.Stretch }; chart.SizeChanged += (_, _) => DrawChart(); plot.Children.Add(chart); body.Children.Add(Card(plot));
                totals = Text("—", 12, "#8E8E93"); Mono(totals); body.Children.Add(totals); break;
            case "Profiles":
                body.Children.Add(Button(T("Import"), Import, true)); profileList = new StackPanel { Spacing = 16 }; body.Children.Add(profileList); break;
            case "Connections":
                body.Children.Add(Text(T("BasicPage"), 12, "#8E8E93"));
                body.Children.Add(Button(T("CloseAll"), async () => { await api.Call("connections", HttpMethod.Delete); }));
                body.Children.Add(Text(T("ConnectionColumns"), 12, "#8E8E93")); list = new ListView { FontSize = 13, SelectionMode = ListViewSelectionMode.None, MinHeight = 150 }; body.Children.Add(list); break;
            case "Logs":
                body.Children.Add(Text(T("BasicPage"), 12, "#8E8E93"));
                var levels = new ComboBox { ItemsSource = new[] { T("AllLevels"), "trace", "debug", "info", "warn", "error", "fatal", "panic" }, SelectedIndex = 0, MinWidth = 150 };
                if (level != "") levels.SelectedItem = level;
                levels.SelectionChanged += (_, _) => { level = levels.SelectedIndex == 0 ? "" : levels.SelectedItem?.ToString() ?? ""; _ = Refresh(); };
                var follow = new ToggleSwitch { Header = T("AutoScroll"), IsOn = autoScroll }; follow.Toggled += (_, _) => autoScroll = follow.IsOn;
                body.Children.Add(Row(levels, follow)); list = new ListView { FontSize = 13, SelectionMode = ListViewSelectionMode.None, Height = 380 }; body.Children.Add(list); break;
            case "Tools":
                var tail = new StackPanel { Spacing = 16 }; tail.Children.Add(Text("Tailscale", 13, "#9A9AA0")); tailStatus = Text(T("Disabled"), 18, "#8E8E93"); tail.Children.Add(tailStatus); tailHint = Text(T("NoTail"), 13, "#9A9AA0"); tail.Children.Add(tailHint);
                host = Text("—"); ips = Text("—"); expiry = Text("—"); sync = Text("—");
                foreach (var pair in new[] { ("Hostname", host), ("TailnetIP", ips), ("KeyExpiry", expiry), ("LastSync", sync) }) { var field = new StackPanel { Spacing = 4 }; field.Children.Add(Text(T(pair.Item1), 12, "#8E8E93")); field.Children.Add(pair.Item2); tail.Children.Add(field); }
                tail.Children.Add(Text(T("HandshakeHint"), 12, "#9A9AA0")); tail.Children.Add(Button(T("Test"), TestTail, true)); testResult = Text(""); tail.Children.Add(testResult); body.Children.Add(Card(tail));
                foreach (string name in new[] { "Tailscale SSH", "USBIP", "OpenConnect", "OpenVPN" }) { var row = new Grid { Opacity = .45 }; row.ColumnDefinitions.Add(new ColumnDefinition()); row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto }); row.Children.Add(Text(name)); var hint = Text(T("Unsupported"), 12, "#8E8E93"); Grid.SetColumn(hint, 1); row.Children.Add(hint); body.Children.Add(Card(row)); } break;
            case "Settings": _ = Run(RenderSettings); break;
        }
    }
    private async Task Core(string action) { await api.Call("core/" + action, HttpMethod.Post); }
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
    private async Task RenderSettings()
    {
        if (!connected) return;
        var settings = await api.Call("settings"); if (page != "Settings") return;
        var body = Body(); body.Children.Clear(); var panel = new StackPanel { Spacing = 16 };
        var autoStart = new ToggleSwitch { Header = T("AutoStart"), IsOn = B(settings, "auto_start") };
        var autoConnect = new ToggleSwitch { Header = T("AutoConnect"), IsOn = B(settings, "auto_connect") };
        var proxy = new ToggleSwitch { Header = T("Proxy"), IsOn = B(state, "system_proxy") };
        proxy.Toggled += async (_, _) => await Run(async () => { await api.Call("proxy", HttpMethod.Post, new { enabled = proxy.IsOn }); });
        var port = new NumberBox { Header = T("Port"), Minimum = 1, Maximum = 65535, Value = N(settings, "port"), SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact };
        var lang = new ComboBox { Header = T("Language"), ItemsSource = new[] { "中文", "English" }, SelectedIndex = language == "en-US" ? 1 : 0 };
        var theme = new ComboBox { Header = T("Theme"), ItemsSource = new[] { T("Dark"), T("Light") }, SelectedIndex = Root.RequestedTheme == ElementTheme.Light ? 1 : 0 };
        async Task Save() { await api.Call("settings", HttpMethod.Post, new { auto_start = autoStart.IsOn, auto_connect = autoConnect.IsOn, port = (int)port.Value, language = lang.SelectedIndex == 1 ? "en-US" : "zh-CN", theme = theme.SelectedIndex == 1 ? "light" : "dark" }); }
        autoStart.Toggled += async (_, _) => await Run(Save); autoConnect.Toggled += async (_, _) => await Run(Save);
        lang.SelectionChanged += async (_, _) => await Run(async () => { await Save(); LoadLanguage(lang.SelectedIndex == 1 ? "en-US" : "zh-CN"); });
        theme.SelectionChanged += async (_, _) => await Run(async () => { await Save(); Root.RequestedTheme = theme.SelectedIndex == 1 ? ElementTheme.Light : ElementTheme.Dark; Render(); });
        var close = new ToggleSwitch { Header = T("CloseExits"), IsOn = closeExits };
        close.Toggled += async (_, _) => await Run(async () => { closeExits = close.IsOn; System.IO.Directory.CreateDirectory(EngineClient.DataDir); await System.IO.File.WriteAllTextAsync(prefsPath, new JsonObject { ["close_exits"] = closeExits }.ToJsonString()); });
        foreach (var element in new UIElement[] { autoStart, autoConnect, proxy, port, Button(T("SavePort"), async () => { if (double.IsNaN(port.Value) || port.Value != Math.Truncate(port.Value) || port.Value < 1 || port.Value > 65535) throw new InvalidOperationException(T("InvalidPort")); await Save(); }), lang, theme, close }) panel.Children.Add(element);
        body.Children.Add(Card(panel)); body.Children.Add(Text(T("BackgroundHint"), 12, "#8E8E93"));
        body.Children.Add(Row(Button(T("OpenData"), () => { Process.Start(new ProcessStartInfo("explorer.exe", EngineClient.DataDir) { UseShellExecute = true }); return Task.CompletedTask; }), Button(T("Quit"), Quit)));
    }
    private async Task Quit() { if (connected) await api.Call("quit", HttpMethod.Post); exiting = true; Application.Current.Exit(); }
    private async Task Refresh()
    {
        if (!connected || refreshing || acting) return;
        refreshing = true;
        string requestedPage = page;
        try {
            state = await api.Call("state");
            if (page != requestedPage) return;
            switch (requestedPage) {
                case "Dashboard":
                    if (status is null || profile is null || totals is null || up is null || down is null) break;
                    bool running = B(state, "running"), error = S(state, "error") != "";
                    status.Text = T(error ? "Error" : running ? "Running" : "Stopped"); status.Foreground = Brush(error ? "#FF453A" : running ? "#30D158" : "#8E8E93");
                    profile.Text = S(state, "profile") == "" ? T("NoProfile") : S(state, "profile");
                    Enable(start!, !running && S(state, "profile") != ""); Enable(stop!, running); Enable(restart!, running);
                    var traffic = running ? await api.Call("traffic") : null;
                    if (page != requestedPage || up is null || down is null || totals is null) break;
                    up.Text = Bytes(N(traffic, "upload")) + "/s"; down.Text = Bytes(N(traffic, "download")) + "/s";
                    totals.Text = $"{T("TotalUpload")} {Bytes(N(traffic, "upload_total"))}    {T("TotalDownload")} {Bytes(N(traffic, "download_total"))}    {T("Connections")} {N(traffic, "connections"):0}";
                    samples.Enqueue((N(traffic, "upload"), N(traffic, "download"))); while (samples.Count > 60) samples.Dequeue(); DrawChart(); break;
                case "Profiles":
                    var profiles = await api.Call("profiles"); if (page != requestedPage || profileList is null) break;
                    profileList.Children.Clear();
                    if (profiles is not JsonArray array || array.Count == 0) { profileList.Children.Add(Card(Text(T("NoProfile"), 13, "#8E8E93"))); break; }
                    foreach (var p in array) {
                        string id = Uri.EscapeDataString(S(p, "id")); var box = new StackPanel { Spacing = 12 };
                        box.Children.Add(Text(S(p, "name"), 18)); box.Children.Add(Text($"{T(S(p, "type") == "remote" ? "Remote" : "Local")} · {Date(S(p, "updated"))}", 12, "#8E8E93"));
                        if (B(p, "enabled")) box.Children.Add(Text(T("Enabled"), 12, "#30D158"));
                        box.Children.Add(Row(Button(T(B(p, "enabled") ? "Disable" : "Enable"), async () => { await api.Call("profiles/" + id, HttpMethod.Put, new { enabled = !B(p, "enabled") }); }), Button(T("Delete"), async () => { await api.Call("profiles/" + id, HttpMethod.Delete); })));
                        profileList.Children.Add(Card(box));
                    } break;
                case "Tools":
                    var tail = await api.Call("tools/tailscale", missingAllowed: true); if (page != requestedPage || tailStatus is null) break;
                    tailHint!.Visibility = tail is null || !B(tail, "enabled") || new[] { "hostname", "key_expiry", "last_sync" }.Any(key => S(tail, key) == "") || tail["ips"] is not JsonArray addresses || addresses.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
                    tailStatus.Text = tail?["enabled"] is null ? "—" : T(!B(tail, "enabled") ? "Disabled" : B(tail, "registered") ? "Registered" : "Unregistered"); tailStatus.Foreground = Brush(B(tail, "enabled") && B(tail, "registered") ? "#30D158" : "#8E8E93");
                    host!.Text = S(tail, "hostname") is "" ? "—" : S(tail, "hostname"); ips!.Text = tail?["ips"] is JsonArray ipArray && ipArray.Count > 0 ? string.Join("\n", ipArray.Select(x => x?.ToString())) : "—"; expiry!.Text = Date(S(tail, "key_expiry")); sync!.Text = "—"; // The API exposes cache sync time, not a handshake timestamp.
                    break;
                case "Connections":
                    var connections = B(state, "running") ? await api.Call("connections") : null;
                    if (page != requestedPage || list is null) break;
                    list.Items.Clear();
                    if (connections?["connections"] is JsonArray cs) foreach (var c in cs) {
                        var meta = c?["metadata"]; var destination = S(meta, "host"); if (destination == "") destination = S(meta, "destinationIP");
                        var duration = DateTimeOffset.TryParse(S(c, "start"), out var since) ? Math.Max(0, (DateTimeOffset.UtcNow - since).TotalSeconds) : 0;
                        var connectionText = Text($"{destination}    {S(meta, "network")}    {Bytes(N(c, "upload"))}    {Bytes(N(c, "download"))}    {duration:0}s"); Mono(connectionText); list.Items.Add(connectionText);
                    } if (list.Items.Count == 0) list.Items.Add(Card(Text(T("Empty") + "\n" + T("EmptyHint"), 13, "#9A9AA0"))); break;
                case "Logs":
                    var logs = await api.Call("logs?limit=500&level=" + Uri.EscapeDataString(level)); if (page != requestedPage || list is null) break;
                    // Keep list items stable while a user is reading older entries.
                    var lines = (logs as JsonArray)?.Select(l => $"{S(l, "time")} [{S(l, "level")}] {S(l, "message")}").ToArray() ?? [];
                    if (!lines.SequenceEqual(list.Items.Cast<string>())) { list.ItemsSource = null; list.Items.Clear(); foreach (var line in lines) list.Items.Add(line); if (autoScroll && list.Items.Count > 0) list.ScrollIntoView(list.Items[list.Items.Count - 1]); } break;
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
