using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Media;
using System.Linq;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage.Pickers;

namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private ConnectionPhase? transition;
    private ConnectionState Session => new(transition ?? (!connected ? ConnectionPhase.Invalid : B(state, "running") ? ConnectionPhase.Connected : ConnectionPhase.Disconnected), connected, S(state, "profile") != "");
    private Button? toolbarStart;
    private readonly Dictionary<string, Border> dashboardCards = new();
    private JsonObject preferences = new();
    private TextBlock? memory, goroutines, inbound, outbound, uploadTotal, downloadTotal;
    private ComboBox? profilePicker;
    private Button? closeAllButton;
    private bool pickingProfile, logsPaused;
    private string connectionSearch = "", logSearch = "", logCursor = "", visibleLogText = "";
    private int connectionFilter = 1, connectionSort;
    private JsonArray logSnapshot = new();
    private CancellationTokenSource? searchDelay;
    private readonly Dictionary<string, JsonObject> connectionHistory = new();
    private StackPanel? groupsPanel;
    private TextBlock? routingSummary;
    private void LoadPreferences()
    {
        try { preferences = JsonNode.Parse(System.IO.File.ReadAllText(prefsPath)) as JsonObject ?? new(); } catch { preferences = new(); }
        connectionFilter = (int)N(preferences, "connection_filter"); if (preferences["connection_filter"] is null) connectionFilter = 1;
        connectionSort = (int)N(preferences, "connection_sort");
    }
    private async Task SavePreferences()
    {
        System.IO.Directory.CreateDirectory(EngineClient.DataDir);
        string tmp = prefsPath + ".tmp";
        await System.IO.File.WriteAllTextAsync(tmp, preferences.ToJsonString());
        System.IO.File.Move(tmp, prefsPath, true);
    }
    private void UpdateControls()
    {
        var session = Session;
        foreach (var item in Nav.MenuItems.OfType<NavigationViewItem>())
            if ((string)item.Tag is "Groups" or "Connections") item.IsEnabled = session.IsConnectedStrict;
        if (!session.IsConnectedStrict && page is "Groups" or "Connections") {
            page = "Dashboard"; Nav.SelectedItem = Nav.MenuItems.OfType<NavigationViewItem>().FirstOrDefault(x => (string)x.Tag == page); Render();
        }
        if (toolbarStart is not null) {
            toolbarStart.Content = !session.IsSwitchable && session.IsConnected ? new ProgressRing { IsActive = true, Width = 20, Height = 20 } : T(session.IsConnected ? "Stop" : "Start");
            Enable(toolbarStart, session.IsEnabled && session.IsSwitchable && !acting);
        }
        if (start is not null) Enable(start, session.IsEnabled && session.IsSwitchable && !session.IsConnected);
        if (stop is not null) Enable(stop, session.IsSwitchable && session.IsConnectedStrict);
        if (restart is not null) Enable(restart, session.IsSwitchable && session.IsConnectedStrict);
        if (profilePicker is not null) profilePicker.IsEnabled = session.IsSwitchable;
        foreach (var action in profileActions) action.IsEnabled = connected && S(state, "profile") != "" && !acting;
        if (profileUpdateButton is not null) profileUpdateButton.IsEnabled = profileUpdateButton.IsEnabled && selectedProfileRemote;
        foreach (var card in dashboardCards) card.Value.Visibility = CardEnabled(card.Key) && (card.Key != "Routing Mode" || multipleRoutingModes) && (card.Key == "Profile" || session.IsConnected) ? Visibility.Visible : Visibility.Collapsed;
    }
    private bool CardEnabled(string key) => key == "Profile" || preferences["cards"]?[key]?.GetValue<bool>() != false;
    private void AddDashboardCard(StackPanel body, string key, UIElement child)
    {
        var panel = new StackPanel { Spacing = 12 }; panel.Children.Add(Text(T(key), 16)); panel.Children.Add(child);
        var border = Card(panel); dashboardCards[key] = border; body.Children.Add(border);
    }
    private void RenderDashboard(StackPanel body)
    {
        dashboardCards.Clear();
        status = Text(T("Stopped"), 24); profile = Text(T("NoProfile"));
        var box = new StackPanel { Spacing = 12 }; box.Children.Add(status);
        var proxyPanel = new StackPanel { Spacing = 10 };
        systemProxyToggle = new ToggleSwitch { Header = T("System Proxy") };
        systemProxyToggle.Toggled += async (_, _) => {
            if (updatingProxy) return;
            await Run(async () => { await api.Call("proxy", HttpMethod.Post, new { enabled = systemProxyToggle.IsOn }); });
            await Refresh();
        };
        proxyHint = Text("", 12, "#9A9AA0");
        proxyPanel.Children.Add(systemProxyToggle); proxyPanel.Children.Add(proxyHint);
        var networkResult = Text("", 12);
        proxyPanel.Children.Add(Button(T("Check Internet"), async () => {
            var result = await api.Call("tools/connectivity", HttpMethod.Post);
            networkResult.Text = B(result, "ok") ? T("Internet OK") + $" · {N(result, "ms"):0} ms" : T("Internet Failed") + " · " + S(result, "error");
            networkResult.Foreground = Brush(B(result, "ok") ? "#30D158" : "#FF453A");
        }));
        proxyPanel.Children.Add(networkResult);
        memory = Text("..."); goroutines = Text("..."); box.Children.Add(Row(Text(T("Memory")), memory, Text("Goroutines"), goroutines)); AddDashboardCard(body, "Status", box);
        inbound = Text("..."); outbound = Text("..."); AddDashboardCard(body, "Connections", Row(Text(T("Inbound")), inbound, Text(T("Outbound")), outbound));
        AddDashboardCard(body, "System Proxy", proxyPanel);
        RenderModeCard(body);
        up = Text("...", 28); uploadTotal = Text("..."); var u = new StackPanel { Spacing = 8 }; u.Children.Add(up); u.Children.Add(uploadTotal); AddDashboardCard(body, "Upload", u);
        down = Text("...", 28); downloadTotal = Text("..."); var d = new StackPanel { Spacing = 8 }; d.Children.Add(down); d.Children.Add(downloadTotal); AddDashboardCard(body, "Download", d);
        chart = new Microsoft.UI.Xaml.Controls.Canvas { Height = 150, HorizontalAlignment = HorizontalAlignment.Stretch }; chart.SizeChanged += (_, _) => DrawChart(); AddDashboardCard(body, "Real-time Speed", chart);
        var profiles = new StackPanel { Spacing = 12 }; profilePicker = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch, PlaceholderText = T("Select Profile") };
        profilePicker.SelectionChanged += async (_, _) => { if (pickingProfile || profilePicker.SelectedItem is not ComboBoxItem item) return; await Run(async () => { transition = Session.IsConnectedStrict ? ConnectionPhase.Reasserting : null; UpdateControls(); try { await api.Call("native/profile", HttpMethod.Put, new { id = (string)item.Tag }); } finally { transition = null; } }); await Refresh(); };
        profiles.Children.Add(profilePicker);
        profileActions.Clear();
        profileSummary = Text("", 12, "#9A9AA0"); profiles.Children.Add(profileSummary);
        profiles.Children.Add(Row(Button(T("Import Profile"), Import), ProfileAction("Edit Profile", () => EditProfile(SelectedProfileID())),
            profileUpdateButton = ProfileAction("Update Profile", () => UpdateProfile(SelectedProfileID()))));
        profiles.Children.Add(Row(Button(T("Manage Profiles"), ManageProfiles), ProfileAction("View Content", () => ProfileContent(SelectedProfileID())),
            ProfileAction("Export Profile", () => ExportProfile(SelectedProfileID())), ProfileAction("Delete", () => DeleteProfile(SelectedProfileID()))));
        AddDashboardCard(body, "Profile", profiles);
        var profileCard = dashboardCards["Profile"]; body.Children.Remove(profileCard); body.Children.Insert(0, profileCard);
        PairDashboardCards(body, "Status", "Connections");
        PairDashboardCards(body, "Upload", "Download");
        UpdateControls();
    }
    private void PairDashboardCards(StackPanel body, string left, string right)
    {
        var a = dashboardCards[left]; var b = dashboardCards[right];
        int index = body.Children.IndexOf(a);
        body.Children.Remove(a); body.Children.Remove(b);
        var grid = new Grid { ColumnSpacing = 16 };
        grid.ColumnDefinitions.Add(new ColumnDefinition());
        grid.ColumnDefinitions.Add(new ColumnDefinition());
        Grid.SetColumn(b, 1); grid.Children.Add(a); grid.Children.Add(b);
        body.Children.Insert(index, grid);
    }
    private async Task ManageCards()
    {
        var panel = new StackPanel { Spacing = 12 };
        foreach (string key in new[] { "Status", "Connections", "Upload", "Download", "Real-time Speed", "System Proxy", "Routing Mode", "Profile" }) {
            var toggle = new CheckBox { Content = T(key), IsChecked = CardEnabled(key), IsEnabled = key != "Profile" };
            toggle.Click += async (_, _) => { if (preferences["cards"] is not JsonObject) preferences["cards"] = new JsonObject(); preferences["cards"]![key] = toggle.IsChecked == true; await Run(SavePreferences); UpdateControls(); }; panel.Children.Add(toggle);
        }
        var dialog = new ContentDialog { XamlRoot = Root.XamlRoot, Title = T("Dashboard Items"), Content = panel, CloseButtonText = T("Done"), SecondaryButtonText = T("Reset") };
        if (await dialog.ShowAsync() == ContentDialogResult.Secondary) { preferences.Remove("cards"); await SavePreferences(); }
        UpdateControls();
    }
    private async Task RefreshDashboard()
    {
        var metrics = await api.Call("native/status"); var traffic = Session.IsConnectedStrict ? await api.Call("traffic") : null;
        var profiles = await api.Call("profiles") as JsonArray;
        if (page != "Dashboard" || status is null) return;
        status.Text = T(S(state, "error") != "" ? "Error" : Session.IsConnectedStrict ? "Running" : Session.IsConnected ? "Connecting..." : "Stopped");
        if (systemProxyToggle is not null) {
            updatingProxy = true;
            systemProxyToggle.IsOn = B(state, "system_proxy");
            systemProxyToggle.IsEnabled = Session.IsConnectedStrict;
            updatingProxy = false;
            proxyHint!.Text = T(!Session.IsConnectedStrict ? "Start to connect" : B(state, "system_proxy") ? "Browser proxy active" : "Proxy off hint");
        }
        profile!.Text = S(state, "profile") == "" ? T("NoProfile") : S(state, "profile");
        memory!.Text = Bytes(N(metrics, "memory")); goroutines!.Text = S(metrics, "goroutines");
        inbound!.Text = S(metrics, "connections_in"); outbound!.Text = S(metrics, "connections_out");
        up!.Text = "↑ " + Bytes(N(traffic, "upload")) + "/s"; down!.Text = "↓ " + Bytes(N(traffic, "download")) + "/s";
        uploadTotal!.Text = Bytes(N(traffic, "upload_total")); downloadTotal!.Text = Bytes(N(traffic, "download_total"));
        samples.Enqueue((N(traffic, "upload"), N(traffic, "download"))); while (samples.Count > 60) samples.Dequeue(); DrawChart();
        if (profileSummary is not null) {
            var current = profiles?.FirstOrDefault(p => B(p, "enabled"));
            selectedProfileRemote = S(current, "url") != "";
            profileSummary.Text = current is null ? T("NoProfile") : T(S(current, "type") == "remote" ? "Remote" : "Local") + " · " + T("Last Updated") + ": " + Date(S(current, "updated"));
        }
        if (profilePicker is not null && !profilePicker.IsDropDownOpen) {
            pickingProfile = true; profilePicker.Items.Clear();
            if (profiles is not null) foreach (var p in profiles) { var item = new ComboBoxItem { Content = S(p, "name"), Tag = S(p, "id") }; profilePicker.Items.Add(item); if (B(p, "enabled")) profilePicker.SelectedItem = item; }
            pickingProfile = false;
        }
        await RefreshModeCard();
        UpdateControls();
    }
    private void RenderGroups(StackPanel body)
    {
        routingSummary=Text("",13,"#9A9AA0");body.Children.Add(routingSummary);
        body.Children.Add(Text(T("Groups Routing Hint"),12,"#9A9AA0"));
        body.Children.Add(Button(T("TestLatency"), async () => { await api.Call("groups/urltest", HttpMethod.Post); }));
        groupsPanel = new StackPanel { Spacing = 16 }; body.Children.Add(groupsPanel);
    }
    private static string StrategyKey(string type) => type switch { "URLTest" => "StrategyURLTest", "Fallback" => "StrategyFallback", "LoadBalance" => "StrategyLoadBalance", "Relay" => "StrategyRelay", "Selector" => "StrategySelector", _ => "StrategyOther" };
    private string groupSignature="";
    private readonly Dictionary<string,TextBlock> groupLatencies=new();
    private readonly Dictionary<string,TextBlock> groupHeaders=new();
    private async Task RefreshGroups()
    {
        var groups=await api.Call("groups") as JsonArray??new();
        var routing=await api.Call("routing");
        if(page!="Groups"||groupsPanel is null)return;
        string mode=S(routing,"mode").ToLowerInvariant();
        routingSummary!.Text=T(mode switch{"rule"=>"Rule Mode","global"=>"Global Mode","direct"=>"Direct Mode",_=>"Unknown Mode"})
            +$" · {T("Route Rules")}: {N(routing,"rules_count"):0} · {T("Final Outbound")}: {S(routing,"final")}";
        int columns=Math.Max(1,(int)((groupsPanel.ActualWidth>0?groupsPanel.ActualWidth:720)/220));
        string signature=columns+"|"+string.Join("|",groups.Select(g=>S(g,"tag")+S(g,"selected")+S(g,"type")+string.Join(",",(g?["members"] as JsonArray??new()).Select(m=>S(m,"tag")+S(m,"type")))));
        if(signature!=groupSignature||groupsPanel.Children.Count==0){
            groupSignature=signature;groupsPanel.Children.Clear();groupLatencies.Clear();groupHeaders.Clear();
            if(groups.Count==0){groupsPanel.Children.Add(Text(T("No data")));return;}
            foreach(var g in groups){
                string tag=S(g,"tag"),type=S(g,"type"),selected=S(g,"selected");
                bool selector=string.Equals(type,"Selector",StringComparison.OrdinalIgnoreCase);
                var header=Text("",15);groupHeaders[tag]=header;
                var nodes=new Grid();
                for(int col=0;col<columns;col++)nodes.ColumnDefinitions.Add(new ColumnDefinition());
                var members=g?["members"] as JsonArray??new();
                for(int i=0;i<members.Count;i++){
                    var m=members[i];string name=S(m,"tag");bool chosen=name==selected;
                    if(i%columns==0)nodes.RowDefinitions.Add(new RowDefinition {Height=GridLength.Auto});
                    var contents=new StackPanel {Spacing=5};
                    var title=Text((chosen?"✓ ":"")+name);title.MaxLines=2;contents.Children.Add(title);
                    var latency=Text("",12);groupLatencies[tag+"\0"+name]=latency;
                    contents.Children.Add(Row(Text(S(m,"type"),11,"#9A9AA0"),latency));
                    FrameworkElement tile;
                    if(selector){
                        var button=Button("",async()=>{await api.Call("groups/"+Uri.EscapeDataString(tag)+"/select",HttpMethod.Put,new{outbound=name});});
                        button.Content=contents;button.HorizontalContentAlignment=HorizontalAlignment.Stretch;Enable(button,!chosen);
                        if(chosen){button.BorderBrush=Brush("#0A84FF");button.BorderThickness=new Thickness(1);}
                        tile=button;
                    }else tile=new Border {Child=contents,Padding=new Thickness(12),CornerRadius=new CornerRadius(8),BorderThickness=new Thickness(chosen?1:0),BorderBrush=Brush("#0A84FF")};
                    var menu=new MenuFlyout();var test=new MenuFlyoutItem {Text=T("Test Node")};
                    test.Click+=async(_,_)=>await Run(async()=>{await api.Call("node/delay",HttpMethod.Post,new{tag=name});});
                    menu.Items.Add(test);tile.ContextFlyout=menu;
                    tile.HorizontalAlignment=HorizontalAlignment.Stretch;tile.Margin=new Thickness(4);Grid.SetRow(tile,i/columns);Grid.SetColumn(tile,i%columns);nodes.Children.Add(tile);
                }
                bool expanded=preferences["groups_collapsed"]?[tag]?.GetValue<bool>()!=true;
                var expander=new Expander {Header=header,Content=nodes,IsExpanded=expanded,HorizontalAlignment=HorizontalAlignment.Stretch,HorizontalContentAlignment=HorizontalAlignment.Stretch};
                async Task Remember(bool collapsed){if(preferences["groups_collapsed"] is not JsonObject)preferences["groups_collapsed"]=new JsonObject();preferences["groups_collapsed"]![tag]=collapsed;await Run(SavePreferences);}
                expander.Expanding+=async(_,_)=>await Remember(false);expander.Collapsed+=async(_,_)=>await Remember(true);
                var panel=new StackPanel {Spacing=8};
                panel.Children.Add(Row(Button(T("Test Group"),async()=>{await api.Call("groups/"+Uri.EscapeDataString(tag)+"/urltest",HttpMethod.Post);}),Text(T(StrategyKey(type))+(selector?"":" · "+T("ReadOnlyGroup")),12,"#9A9AA0")));
                panel.Children.Add(expander);groupsPanel.Children.Add(Card(panel));
            }
        }
        foreach(var g in groups){
            string tag=S(g,"tag");
            if(groupHeaders.TryGetValue(tag,out var header))header.Text=tag+" · "+S(g,"type")+" · "+(g?["members"] as JsonArray)?.Count+" · "+S(g,"selected");
            foreach(var m in g?["members"] as JsonArray??new()){
                if(!groupLatencies.TryGetValue(tag+"\0"+S(m,"tag"),out var latency))continue;
                double delay=N(m,"delay");latency.Text=delay<=0?"—":$"{delay:0} ms";
                latency.Foreground=Brush(delay<=0?"#9A9AA0":delay<800?"#30D158":delay<1500?"#FF9F0A":"#FF453A");
            }
        }
    }
    private ComboBox Choices(string header, string[] items, int index, Action<int> changed)
    {
        var box = new ComboBox { Header = T(header), ItemsSource = items.Select(T).ToArray(), SelectedIndex = index, MinWidth = 130 };
        box.SelectionChanged += (_, _) => changed(box.SelectedIndex); return box;
    }
    private void RenderConnections(StackPanel body)
    {
        var search = new TextBox { PlaceholderText = T("Search"), Text = connectionSearch };
        search.TextChanged += (_, _) => { connectionSearch = search.Text; ShowConnections(); };
        var filter = Choices("State", ["All", "Active", "Closed"], connectionFilter, i => { connectionFilter = i; preferences["connection_filter"] = i; _ = Run(SavePreferences); ShowConnections(); });
        var sort = Choices("Sort By", ["Date", "Traffic", "Traffic Total"], connectionSort, i => { connectionSort = i; preferences["connection_sort"] = i; _ = Run(SavePreferences); ShowConnections(); });
        var closeAll = Button(T("Close All Connections"), async () => { await api.Call("connections", HttpMethod.Delete); });
        closeAllButton = closeAll;
        body.Children.Add(Row(filter, sort, closeAll));
        body.Children.Add(search); body.Children.Add(ConnectionRow([T("Chain") + "/" + T("Outbound"), T("Network"), T("Source") + " → " + T("Destination"), T("Rule"), T("Upload") + "/" + T("Download"), T("Duration")]));
        list = new ListView { SelectionMode = ListViewSelectionMode.None, HorizontalContentAlignment = HorizontalAlignment.Stretch }; body.Children.Add(list);
    }
    private Grid ConnectionRow(string[] columns)
    {
        var row = new Grid { ColumnSpacing = 8, Padding = new Thickness(0, 8, 0, 8) };
        double[] widths = [1.2, .6, 2, 1, 1.3, .7];
        for (int i = 0; i < columns.Length; i++) { row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(widths[i], GridUnitType.Star) }); var text = Text(columns[i], 12); Grid.SetColumn(text, i); row.Children.Add(text); }
        return row;
    }
    private async Task RefreshConnections()
    {
        var result = await api.Call("connections"); if (page != "Connections") return;
        var now = DateTimeOffset.UtcNow; var active = new HashSet<string>();
        if (result?["connections"] is JsonArray connections) foreach (var c in connections.OfType<JsonObject>()) {
            string id = S(c, "id"); active.Add(id); var clone = (JsonObject)c.DeepClone();
            double elapsed = connectionHistory.TryGetValue(id, out var previous) ? Math.Max(.001, (now - DateTimeOffset.Parse(S(previous, "sampled"))).TotalSeconds) : 1;
            clone["speed"] = previous is null ? 0 : Math.Max(0, (N(c, "upload") + N(c, "download") - N(previous, "upload") - N(previous, "download")) / elapsed);
            clone["sampled"] = now.ToString("O"); clone["closed"] = false; connectionHistory[id] = clone;
        }
        foreach (var pair in connectionHistory) if (!active.Contains(pair.Key) && !B(pair.Value, "closed")) { pair.Value["closed"] = true; pair.Value["end"] = now.ToString("O"); pair.Value["speed"] = 0; }
        foreach (string key in connectionHistory.Where(x => B(x.Value, "closed")).OrderByDescending(x => S(x.Value, "end")).Skip(3000).Select(x => x.Key).ToArray()) connectionHistory.Remove(key);
        ShowConnections();
    }
    private bool MatchesConnection(JsonObject c)
    {
        var m = c["metadata"];
        foreach (string token in connectionSearch.Split(' ', StringSplitOptions.RemoveEmptyEntries)) {
            int colon = token.IndexOf(':'); string key = colon < 0 ? "" : token[..colon], value = colon < 0 ? token : token[(colon + 1)..];
            string haystack = key switch {
                "network" => S(m, "network"), "inbound" => S(m, "inboundName"), "inbound.type" => S(m, "type"),
                "source" => S(m, "sourceIP") + ":" + S(m, "sourcePort"),
                "destination" => S(m, "destinationIP") + ":" + S(m, "destinationPort") + " " + S(m, "host"),
                "" => c.ToJsonString(), _ => ""
            };
            if (!haystack.Contains(value, StringComparison.OrdinalIgnoreCase)) return false;
        }
        return true;
    }
    private void ShowConnections()
    {
        if (page != "Connections" || list is null) return;
        var items = connectionHistory.Values.Where(c => (connectionFilter == 0 || B(c, "closed") == (connectionFilter == 2)) && MatchesConnection(c));
        items = connectionSort switch { 1 => items.OrderByDescending(c => N(c, "speed")), 2 => items.OrderByDescending(c => N(c, "upload") + N(c, "download")), _ => items.OrderByDescending(c => S(c, "start")) };
        list.Items.Clear();
        foreach (var c in items) {
            var m = c["metadata"]; string chain = c["chains"] is JsonArray chains ? string.Join(" → ", chains.Select(x => x?.ToString())) : "";
            string destination = S(m, "host"); if (destination == "") destination = S(m, "destinationIP");
            var end = B(c, "closed") && DateTimeOffset.TryParse(S(c, "end"), out var closed) ? closed : DateTimeOffset.UtcNow;
            double duration = DateTimeOffset.TryParse(S(c, "start"), out var since) ? Math.Max(0, (end - since).TotalSeconds) : 0;
            var line = ConnectionRow([chain, S(m, "network"), $"{S(m, "sourceIP")}:{S(m, "sourcePort")} → {destination}:{S(m, "destinationPort")}", S(c, "rule") + " " + S(c, "rulePayload"), $"↑{Bytes(N(c, "upload"))} ↓{Bytes(N(c, "download"))}\n{Bytes(N(c, "speed"))}/s", $"{duration:0}s\n{T(B(c, "closed") ? "Closed" : "Active")}"]);
            var snapshot=(JsonObject)c.DeepClone();
            var button=Button("",()=>ShowConnectionDetails(snapshot));
            button.Content=line;button.HorizontalAlignment=HorizontalAlignment.Stretch;button.HorizontalContentAlignment=HorizontalAlignment.Stretch;
            list.Items.Add(button);
        }
        if (closeAllButton is not null) closeAllButton.IsEnabled = connectionHistory.Values.Any(c => !B(c, "closed"));
        if (list.Items.Count == 0) {
            var empty = Text(T(connectionSearch != "" ? "No matching connections" : connectionFilter == 1 ? "No active connections" : connectionFilter == 2 ? "No closed connections" : "No connections"), 13, "#8E8E93");
            empty.HorizontalAlignment = HorizontalAlignment.Center;
            empty.TextAlignment = TextAlignment.Center;
            empty.Margin = new Thickness(0, 28, 0, 0);
            list.Items.Add(empty);
        }
    }
    private void RenderLogs(StackPanel body)
    {
        var search = new TextBox { PlaceholderText = T("Search"), Text = logSearch };
        search.TextChanged += async (_, _) => { searchDelay?.Cancel(); var delay = searchDelay = new(); try { await Task.Delay(300, delay.Token); logSearch = search.Text; ShowLogs(); } catch (OperationCanceledException) { } };
        var levels = new ComboBox { Header = T("Log Level"), ItemsSource = new[] { T("Default"), "Error", "Warn", "Info", "Debug", "Trace" }, SelectedIndex = Array.IndexOf(new[] { "", "error", "warn", "info", "debug", "trace" }, level), MinWidth = 110 };
        levels.SelectionChanged += (_, _) => { level = new[] { "", "error", "warn", "info", "debug", "trace" }[levels.SelectedIndex]; ShowLogs(); };
        Button? pause = null; pause = Button(T(logsPaused ? "Resume" : "Pause"), () => { logsPaused = !logsPaused; pause!.Content = T(logsPaused ? "Resume" : "Pause"); return Task.CompletedTask; });
        body.Children.Add(Row(levels, pause, Button(T("To Clipboard"), () => { var data = new DataPackage(); data.SetText(visibleLogText); Clipboard.SetContent(data); return Task.CompletedTask; }), Button(T("To File"), ExportLogs), Button(T("Clear Logs"), async () => { await api.Call("logs", HttpMethod.Delete); logSnapshot = new(); logCursor = ""; logsPaused = false; Render(); })));
        body.Children.Add(search); list = new ListView { SelectionMode = ListViewSelectionMode.None, Height = 340 }; body.Children.Add(list); ShowLogs();
    }
    private static int LogRank(string value) => value.ToLowerInvariant() switch { "panic" or "fatal" => 0, "error" => 1, "warn" => 2, "info" => 3, "debug" => 4, "trace" => 5, _ => 3 };
    private async Task RefreshLogs()
    {
        if (logsPaused) return;
        // A separate request allows long polling without stalling the UI/state timer.
        if (logPolling) return;
        logPolling = true;
        try {
            var logs = await api.Call("logs?limit=3000&wait=1&after=" + Uri.EscapeDataString(logCursor)) as JsonArray ?? new();
            if (logs.Count > 0) logCursor = logs[^1]!.ToJsonString(new System.Text.Json.JsonSerializerOptions { WriteIndented = false });
            if (!logsPaused) { logSnapshot = logs; ShowLogs(); }
        } finally { logPolling = false; }
    }
    private bool logPolling;
    private static readonly Regex Ansi = new("\\x1B\\[([0-9;]*)m", RegexOptions.Compiled);
    private TextBlock ColoredLog(string line, string levelName)
    {
        var text = Text("", 12); Mono(text); text.IsTextSelectionEnabled = true;
        string? color = levelName is "error" or "fatal" ? "#FF453A" : levelName == "warn" ? "#FF9F0A" : null;
        int position = 0;
        foreach (Match match in Ansi.Matches(line)) {
            var run = new Run { Text = line[position..match.Index] }; if (color is not null) run.Foreground = Brush(color); text.Inlines.Add(run);
            foreach (string code in match.Groups[1].Value.Split(';')) color = code switch { "0" or "39" or "" => null, "30" or "90" => "#8E8E93", "31" or "91" => "#FF453A", "32" or "92" => "#30D158", "33" or "93" => "#FF9F0A", "34" or "94" => "#0A84FF", "35" or "95" => "#BF5AF2", "36" or "96" => "#64D2FF", "37" or "97" => "#AEAEB2", _ => color };
            position = match.Index + match.Length;
        }
        var tail = new Run { Text = line[position..] }; if (color is not null) tail.Foreground = Brush(color); text.Inlines.Add(tail); return text;
    }
    private void ShowLogs()
    {
        if (page != "Logs" || list is null) return;
        int threshold = LogRank(level == "" ? coreLogLevel : level);
        var logs = logSnapshot.Where(l => LogRank(S(l, "level")) <= threshold && S(l, "message").Contains(logSearch, StringComparison.OrdinalIgnoreCase)).TakeLast(1000).ToArray();
        var lines = logs.Select(l => $"{S(l, "time")} [{S(l, "level")}] {S(l, "message")}").ToArray();
        string plain = string.Join("\n", lines.Select(line => Ansi.Replace(line, "")));
        if (plain == visibleLogText && list.Items.Count > 0) return;
        visibleLogText = plain; list.Items.Clear();
        for (int i = 0; i < lines.Length; i++) list.Items.Add(ColoredLog(lines[i], S(logs[i], "level")));
        if (list.Items.Count == 0) list.Items.Add(Text(T(Session.IsConnectedStrict ? "Empty logs" : Session.IsGracePeriodOrEmpty && Session.IsConnected ? "Connecting..." : "Service not started")));
        if (!logsPaused && list.Items.Count > 0) list.ScrollIntoView(list.Items[^1]);
    }
    private async Task ExportLogs()
    {
        var picker = new FileSavePicker { SuggestedFileName = $"logs-{DateTime.Now:yyyy-MM-dd-HH-mm-ss}-{Guid.NewGuid()}" };
        picker.FileTypeChoices.Add("Text", new List<string> { ".txt" });
        WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(this));
        var file = await picker.PickSaveFileAsync(); if (file is not null) await Windows.Storage.FileIO.WriteTextAsync(file, visibleLogText);
    }
    private string coreLogLevel = "info";
    private async Task RenderSettings()
    {
        if (!connected) return;
        var settings = await api.Call("settings"); var core = await api.Call("native/core"); var metrics = await api.Call("native/status");
        string version = "", coreVersion = "";
        try { var info = await api.Call("tools/info"); version = S(info, "version"); coreVersion = S(info, "core_version"); } catch { }
        if (page != "Settings") return;
        coreLogLevel = S(core, "log_level"); var body = Body(); body.Children.Clear();
        var app = new StackPanel { Spacing = 12 }; app.Children.Add(Text(T("App"), 18));
        var lang = Choices("Language", ["简体中文", "English"], language == "en-US" ? 1 : 0, _ => { });
        var theme = Choices("Theme", ["Dark", "Light", "System Default"], S(settings, "theme") switch { "light" => 1, "system" => 2, _ => 0 }, _ => { });
        var autoStart = new ToggleSwitch { Header = T("Start At Login"), IsOn = B(settings, "auto_start") };
        async Task SaveApp() { await api.Call("settings", HttpMethod.Post, new { auto_start = autoStart.IsOn, language = lang.SelectedIndex == 1 ? "en-US" : "zh-CN", theme = new[] { "dark", "light", "system" }[theme.SelectedIndex] }); }
        autoStart.Toggled += async (_, _) => await Run(SaveApp);
        lang.SelectionChanged += async (_, _) => await Run(async () => { await SaveApp(); LoadLanguage(lang.SelectedIndex == 1 ? "en-US" : "zh-CN"); });
        theme.SelectionChanged += async (_, _) => await Run(async () => { await SaveApp(); Root.RequestedTheme = theme.SelectedIndex switch { 1 => ElementTheme.Light, 2 => ElementTheme.Default, _ => ElementTheme.Dark }; Render(); });
        app.Children.Add(lang); app.Children.Add(theme); app.Children.Add(autoStart);
        var autoConnect = new ToggleSwitch { Header = T("Auto Connect"), IsOn = B(settings, "auto_connect") };
        autoConnect.Toggled += async (_, _) => await Run(async () => { await api.Call("settings", HttpMethod.Post, new { auto_connect = autoConnect.IsOn }); });
        var autoProxy = new ToggleSwitch { Header = T("Auto Proxy"), IsOn = settings?["auto_proxy"] is null || B(settings, "auto_proxy") };
        autoProxy.Toggled += async (_, _) => await Run(async () => { await api.Call("settings", HttpMethod.Post, new { auto_proxy = autoProxy.IsOn }); });
        var closeMode = new ToggleSwitch { Header = T("Close to Tray"), IsOn = !closeExits };
        closeMode.Toggled += async (_, _) => await Run(async () => { closeExits = !closeMode.IsOn; preferences["close_exits"] = closeExits; await SavePreferences(); });
        app.Children.Add(autoConnect); app.Children.Add(autoProxy); app.Children.Add(closeMode);
        app.Children.Add(Text(T("Tray Hint"), 12, "#9A9AA0"));
        app.Children.Add(Button(T("Quit Client"), Quit));
        body.Children.Add(Card(app));
        var corePanel = new StackPanel { Spacing = 12 }; corePanel.Children.Add(Text(T("Core"), 18));
        string corePath = S(metrics, "core_path");
        string shown = string.IsNullOrWhiteSpace(corePath) ? T("Bundled core") : System.IO.Path.GetFileName(corePath);
        var coreBox = new TextBox { Header = T("Core Path"), Text = shown, IsReadOnly = true };
        ToolTipService.SetToolTip(coreBox, corePath);
        corePanel.Children.Add(coreBox);
        var logLevel = Choices("Log Level", ["error", "warn", "info", "debug", "trace"], Array.IndexOf(new[] { "error", "warn", "info", "debug", "trace" }, coreLogLevel), _ => { });
        logLevel.SelectionChanged += async (_, _) => await Run(async () => { await api.Call("native/core", HttpMethod.Put, new { log_level = logLevel.SelectedItem?.ToString() }); coreLogLevel = logLevel.SelectedItem?.ToString() ?? "info"; }); corePanel.Children.Add(logLevel); body.Children.Add(Card(corePanel));
        var overrides = new StackPanel { Spacing = 12 }; overrides.Children.Add(Text(T("Profile Override"), 18));
        foreach (string name in new[] { "No Default Route", "Exclude Local Networks", "Strict Route" }) overrides.Children.Add(new ToggleSwitch { Header = T(name), IsEnabled = false });
        overrides.Children.Add(Text(T("TUN is unavailable"), 12)); body.Children.Add(Card(overrides));
        var about = new StackPanel { Spacing = 6 }; about.Children.Add(Text(T("About"), 18));
        var product = Text("sing-box for Windows", 15); product.FontWeight = Microsoft.UI.Text.FontWeights.SemiBold; about.Children.Add(product);
        if (version != "" && coreVersion != "") about.Children.Add(Text($"v{version} · sing-box core {coreVersion}", 12, "#8E8E93"));
        body.Children.Add(Card(about));
    }
}
