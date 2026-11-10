using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using System.Text.Json.Nodes;
using Windows.ApplicationModel.DataTransfer;
using Windows.Storage.Pickers;

namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private TextBlock? profileSummary;
    private readonly List<Button> profileActions = new();
    private Button? profileUpdateButton;
    private bool selectedProfileRemote;
    private Button ProfileAction(string title, Func<Task> action) {
        var button = Button(T(title), action); profileActions.Add(button); return button;
    }
    private string SelectedProfileID() => profilePicker?.SelectedItem is ComboBoxItem item ? (string)item.Tag : throw new InvalidOperationException(T("NoProfile"));
    private ContentDialog ProfileDialog(string title, UIElement content, string? primary = null) => new() {
        XamlRoot = Root.XamlRoot, RequestedTheme = Root.ActualTheme, Title = title, Content = content,
        PrimaryButtonText = primary ?? "", CloseButtonText = T("Cancel"), DefaultButton = primary is null ? ContentDialogButton.Close : ContentDialogButton.Primary
    };
    private async Task EditProfile(string id)
    {
        var detail = await api.Call("profiles/" + id);
        var p = detail?["profile"];
        var settings = await api.Call("settings");
        var name = new TextBox { Header = T("Name"), Text = S(p, "name") };
        var url = new TextBox { Header = T("URL"), Text = S(p, "url"), PlaceholderText = "https://", IsEnabled = S(p,"type")=="remote" };
        var auto = new ToggleSwitch { Header = T("Profile Auto Update"), IsOn = p?["auto_update"] is null ? N(settings, "update_hours") > 0 : B(p, "auto_update") };
        double interval = N(p, "update_minutes"); if (interval <= 0) interval = Math.Max(60, N(settings, "update_hours") * 60);
        var minutes = new NumberBox { Header = T("Update Minutes"), Value = interval, Minimum = 1, Maximum = 525600, SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Compact };
        void UpdateAvailability() { auto.IsEnabled = !string.IsNullOrWhiteSpace(url.Text); minutes.IsEnabled = auto.IsEnabled && auto.IsOn; }
        url.TextChanged += (_, _) => UpdateAvailability(); auto.Toggled += (_, _) => UpdateAvailability(); UpdateAvailability();
        var panel = new StackPanel { Spacing = 12, MinWidth = 420 };
        panel.Children.Add(name); panel.Children.Add(Text(T(S(p, "type") == "remote" ? "Remote" : "Local"), 12));
        panel.Children.Add(url); panel.Children.Add(auto); panel.Children.Add(minutes);
        panel.Children.Add(Text(T("Last Updated") + ": " + Date(S(p, "updated")), 12));
        var error = Text("", 12, "#FF453A"); panel.Children.Add(error);
        var dialog = ProfileDialog(T("Edit Profile"), panel, T("Save"));
        dialog.PrimaryButtonClick += async (_, args) => {
            var deferral = args.GetDeferral();
            try {
                if (string.IsNullOrWhiteSpace(name.Text)) throw new InvalidOperationException(T("Name Required"));
                if (url.Text != "" && (!Uri.TryCreate(url.Text, UriKind.Absolute, out var u) || u.Scheme is not ("http" or "https"))) throw new InvalidOperationException(T("InvalidURL"));
                if (double.IsNaN(minutes.Value) || minutes.Value != Math.Floor(minutes.Value)) throw new InvalidOperationException(T("Invalid Interval"));
                await api.Call("profiles/" + id, HttpMethod.Put, new { name = name.Text, url = url.Text, auto_update = auto.IsEnabled && auto.IsOn, update_minutes = (int)minutes.Value });
            } catch (Exception ex) { args.Cancel = true; error.Text = ex.Message; }
            finally { deferral.Complete(); }
        };
        await dialog.ShowAsync();
    }
    private async Task ProfileContent(string id)
    {
        var detail = await api.Call("profiles/" + id);
        bool local = S(detail?["profile"], "type") == "local";
        var content = new TextBox { Text = S(detail, "content"), AcceptsReturn = true, TextWrapping = TextWrapping.NoWrap,
            IsReadOnly = !local, MinWidth = 520, Height = 360, FontFamily = new FontFamily("Cascadia Mono, Consolas"), FontSize = 12 };
        ScrollViewer.SetHorizontalScrollBarVisibility(content, ScrollBarVisibility.Auto);
        var panel = new StackPanel { Spacing = 10 }; panel.Children.Add(Text(T(local ? "Local Content Hint" : "Remote Content Hint"), 12)); panel.Children.Add(content);
        var error = Text("", 12, "#FF453A"); panel.Children.Add(error);
        var dialog = ProfileDialog(T("View Content"), panel, local ? T("Save") : null);
        if (local) dialog.PrimaryButtonClick += async (_, args) => {
            var deferral = args.GetDeferral();
            try { await api.Call("profiles/" + id, HttpMethod.Put, new { content = content.Text }); }
            catch (Exception ex) { args.Cancel = true; error.Text = ex.Message; }
            finally { deferral.Complete(); }
        };
        await dialog.ShowAsync();
    }
    private async Task UpdateProfile(string id)
    {
        await api.Call("profiles/" + id + "/update", HttpMethod.Post);
        var dialog = ProfileDialog(T("Update Complete"), Text(T("Update Applied")), T("Done"));
        await dialog.ShowAsync();
    }
    private async Task DeleteProfile(string id)
    {
        var detail = await api.Call("profiles/" + id);
        bool active = B(detail?["profile"], "enabled");
        var message = S(detail?["profile"], "name") + "\n\n" + T(active && B(state, "running") ? "Delete Active Hint" : "Delete Profile Hint");
        var dialog = ProfileDialog(T("Delete Profile"), Text(message), T("Delete"));
        dialog.DefaultButton = ContentDialogButton.Close;
        if (await dialog.ShowAsync() != ContentDialogResult.Primary) return;
        if (active && B(state, "running")) await Core("stop");
        await api.Call("profiles/" + id, HttpMethod.Delete);
    }
    private async Task ExportProfile(string id)
    {
        var detail = await api.Call("profiles/" + id);
        var picker = new FileSavePicker { SuggestedFileName = "sing-box-config" };
        picker.FileTypeChoices.Add("JSON", new List<string> { ".json" });
        WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(this));
        var file = await picker.PickSaveFileAsync();
        if (file is not null) await Windows.Storage.FileIO.WriteTextAsync(file, S(detail, "content"));
    }
    private async Task ManageProfiles()
    {
        while (true) {
            var profiles = await api.Call("profiles") as JsonArray;
            var panel = new StackPanel { Spacing = 16, MinWidth = 510 };
            string chosen = "", operation = "";
            var dialog = ProfileDialog(T("Manage Profiles"), new ScrollViewer { Content = panel, MaxHeight = 440 });
            if (profiles is null || profiles.Count == 0) panel.Children.Add(Text(T("NoProfile")));
            if (profiles is not null) foreach (var p in profiles) {
                string id = S(p, "id");
                var entry = new StackPanel { Spacing = 8 };
                entry.Children.Add(Text(S(p, "name") + (B(p, "enabled") ? " · " + T("Enabled") : ""), 16));
                entry.Children.Add(Text(T(S(p, "type") == "remote" ? "Remote" : "Local") + " · " + Date(S(p, "updated")), 12, "#9A9AA0"));
                Button Action(string key, string value, bool enabled = true) {
                    var b = new Button { Content = T(key), IsEnabled = enabled };
                    b.Click += (_, _) => { chosen = id; operation = value; dialog.Hide(); };
                    return b;
                }
                entry.Children.Add(Row(Action("Enable", "enable", !B(p, "enabled")), Action("Edit Profile", "edit"), Action("View Content", "content")));
                entry.Children.Add(Row(Action("Update Profile", "update", S(p, "url") != ""), Action("Export Profile", "export"), Action("Delete", "delete")));
                panel.Children.Add(Card(entry));
            }
            await dialog.ShowAsync();
            if (chosen == "") break;
            switch (operation) {
                case "enable": await api.Call("native/profile", HttpMethod.Put, new { id = chosen }); break;
                case "edit": await EditProfile(chosen); break;
                case "content": await ProfileContent(chosen); break;
                case "update": await UpdateProfile(chosen); break;
                case "export": await ExportProfile(chosen); break;
                case "delete": await DeleteProfile(chosen); break;
            }
            state = await api.Call("state");
        }
    }
}
