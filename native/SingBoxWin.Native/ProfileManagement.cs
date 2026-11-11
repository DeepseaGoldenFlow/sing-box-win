using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
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
    // Export offers JSON or BPF; the engine renders both so the BPF container
    // always matches upstream libbox. The file name carries profile name +
    // timestamp, matching SFM's SaveFile/ShareFile behaviour.
    private async Task ExportProfile(string id)
    {
        var detail = await api.Call("profiles/" + id);
        string name = S(detail?["profile"], "name");
        var choice = new ContentDialog {
            XamlRoot = Root.XamlRoot, RequestedTheme = Root.ActualTheme, Title = T("Export Profile"),
            Content = Text(T("Export Format Hint"), 13), PrimaryButtonText = T("BPF"), SecondaryButtonText = T("JSON"),
            CloseButtonText = T("Cancel"), DefaultButton = ContentDialogButton.Secondary
        };
        var result = await choice.ShowAsync();
        if (result == ContentDialogResult.None) return;
        await SaveProfileFile(id, string.IsNullOrWhiteSpace(name) ? "profile" : name, result == ContentDialogResult.Primary ? "bpf" : "json");
    }

    // ShowQRCode displays the engine-rendered QR image. The engine decides the
    // payload (BPF container, or the deep-link fallback when it does not fit),
    // so the UI only renders the PNG and repeats the engine's downgrade note.
    private async Task ShowQRCode(string id)
    {
        var share = await QRShare(id);
        var image = new BitmapImage();
        // DataWriter is the WinRT-safe way to fill an in-memory stream on .NET 8;
        // byte[].AsBuffer() lives in an assembly that modern WinUI projects omit.
        var stream = new Windows.Storage.Streams.InMemoryRandomAccessStream();
        using (var writer = new Windows.Storage.Streams.DataWriter(stream)) {
            writer.WriteBytes(share.image);
            await writer.StoreAsync();
            await writer.FlushAsync();
            writer.DetachStream();
        }
        stream.Seek(0);
        await image.SetSourceAsync(stream);
        var panel = new StackPanel { Spacing = 12, MinWidth = 340, HorizontalAlignment = HorizontalAlignment.Center };
        panel.Children.Add(new Image { Source = image, Width = 320, Height = 320, Stretch = Stretch.Uniform });
        // The engine reports a downgrade; the wording lives in the string
        // tables so the note follows the UI language rather than the engine's.
        bool fallback = share.format == "deep-link";
        panel.Children.Add(Text(T(fallback ? "QR Deep Link Fallback" : "QR Code Hint"), 12, fallback ? "#FF9F0A" : "#9A9AA0"));
        var error = Text("", 12, "#FF453A"); panel.Children.Add(error);
        var dialog = ProfileDialog(T("Show QR Code"), panel, T("Save QR Code"));
        dialog.PrimaryButtonClick += async (_, args) => {
            var deferral = args.GetDeferral();
            try { await SaveQRCode(share.image); }
            catch (Exception ex) { args.Cancel = true; error.Text = ex.Message; }
            finally { deferral.Complete(); }
        };
        await dialog.ShowAsync();
    }

    // QRShare fetches the engine-rendered PNG. The engine returns base64, so
    // the native client never encodes a QR itself and the displayed and saved
    // images are byte-identical.
    private async Task<(byte[] image, string format, string note)> QRShare(string id)
    {
        var share = await api.Call("profiles/" + id + "/qr");
        return (Convert.FromBase64String(S(share, "image")), S(share, "format"), S(share, "note"));
    }

    // SaveQRCode writes the exact PNG the dialog shows, so a saved file cannot
    // drift from the displayed symbol.
    private async Task SaveQRCode(byte[] image)
    {
        var picker = new FileSavePicker { SuggestedFileName = "sing-box-profile-" + DateTime.Now.ToString("yyyyMMdd-HHmmss") };
        picker.FileTypeChoices.Add("PNG", new List<string> { ".png" });
        WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(this));
        var file = await picker.PickSaveFileAsync();
        if (file is null) return;
        await Windows.Storage.FileIO.WriteBytesAsync(file, image);
    }

    // ImportFromQRImage sends the picked image to the engine, which decodes it
    // and reuses the ordinary import validation (size caps, unique names).
    private async Task ImportFromQRImage()
    {
        var picker = new FileOpenPicker();
        picker.FileTypeFilter.Add(".png");
        picker.FileTypeFilter.Add(".jpg");
        picker.FileTypeFilter.Add(".jpeg");
        WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(this));
        var file = await picker.PickSingleFileAsync();
        if (file is null) return;
        if ((await file.GetBasicPropertiesAsync()).Size > 8 * 1024 * 1024) throw new InvalidOperationException(T("QR Image Too Large"));
        var buffer = await Windows.Storage.FileIO.ReadBufferAsync(file);
        var data = new byte[buffer.Length];
        using (var reader = Windows.Storage.Streams.DataReader.FromBuffer(buffer)) reader.ReadBytes(data);
        await api.Call("profiles/import/qr", HttpMethod.Post, new { data = Convert.ToBase64String(data) });
    }

    // SaveProfileFile streams the engine-rendered bytes (JSON or BPF) to disk.
    // The picker's default name carries profile name + timestamp, matching
    // SFM's SaveFile/ShareFile behaviour, and the engine picks the final name.
    private async Task SaveProfileFile(string id, string name, string format)
    {
        var picker = new FileSavePicker { SuggestedFileName = $"{name}-{DateTime.Now:yyyyMMdd-HHmmss}" };
        picker.FileTypeChoices.Add(format == "bpf" ? "BPF" : "JSON", new List<string> { "." + format });
        WinRT.Interop.InitializeWithWindow.Initialize(picker, WinRT.Interop.WindowNative.GetWindowHandle(this));
        var file = await picker.PickSaveFileAsync();
        if (file is null) return;
        using var response = await api.Raw("profiles/" + id + "/export?format=" + format);
        if (!response.IsSuccessStatusCode) throw new InvalidOperationException((await response.Content.ReadAsStringAsync()).Trim());
        using var stream = await file.OpenStreamForWriteAsync();
        stream.SetLength(0);
        await response.Content.CopyToAsync(stream);
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
                entry.Children.Add(Row(Action("Update Profile", "update", S(p, "url") != ""), Action("Export Profile", "export"), Action("Show QR Code", "qr"), Action("Delete", "delete")));
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
                case "qr": await ShowQRCode(chosen); break;
                case "delete": await DeleteProfile(chosen); break;
            }
            state = await api.Call("state");
        }
    }
}
