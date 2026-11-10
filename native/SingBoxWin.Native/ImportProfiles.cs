using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Storage.Pickers;
namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private async Task ImportProfileDialog()
    {
        var name = new TextBox { Header = T("Name") };
        var mode = new ComboBox { Header = T("Import Method"), ItemsSource = new[] { T("Subscription URL"), T("Paste JSON"), T("Import File") }, SelectedIndex = 0, HorizontalAlignment = HorizontalAlignment.Stretch };
        var url = new TextBox { Header = T("URL"), PlaceholderText = "https://" };
        var content = new TextBox { Header = T("Configuration JSON"), AcceptsReturn = true, Height = 220, TextWrapping = TextWrapping.NoWrap, Visibility = Visibility.Collapsed };
        var choose = new Button { Content = T("Choose File"), Visibility = Visibility.Collapsed };
        var fileHint = Text("", 12, "#9A9AA0"); fileHint.Visibility = Visibility.Collapsed;
        var error = Text("",12,"#FF453A");
        // A file opened through FileOpenPicker is a broker handle: the engine
        // (a separate process) cannot reopen it by path, so bytes are posted.
        Windows.Storage.StorageFile? picked = null;
        string pickedFormat = "";
        mode.SelectionChanged += (_,_) => {
            url.Visibility = mode.SelectedIndex == 0 ? Visibility.Visible : Visibility.Collapsed;
            content.Visibility = mode.SelectedIndex == 0 ? Visibility.Collapsed : Visibility.Visible;
            content.IsReadOnly = mode.SelectedIndex == 2;
            choose.Visibility = mode.SelectedIndex == 2 ? Visibility.Visible : Visibility.Collapsed;
            fileHint.Visibility = mode.SelectedIndex == 2 ? Visibility.Visible : Visibility.Collapsed;
        };
        choose.Click += async (_,_) => {
            try {
                var picker=new FileOpenPicker(); picker.FileTypeFilter.Add(".json"); picker.FileTypeFilter.Add(".bpf");
                WinRT.Interop.InitializeWithWindow.Initialize(picker,WinRT.Interop.WindowNative.GetWindowHandle(this));
                var file=await picker.PickSingleFileAsync();
                if(file is null)return;
                if((await file.GetBasicPropertiesAsync()).Size>16*1024*1024)throw new InvalidOperationException(T("File Too Large"));
                picked = file; pickedFormat = file.FileType.ToLowerInvariant();
                content.Text = pickedFormat == ".bpf" ? T("BPF File Selected") : await Windows.Storage.FileIO.ReadTextAsync(file);
                fileHint.Text = pickedFormat == ".bpf" ? T("BPF File Hint") : T("JSON File Hint");
                // BPF carries its own profile name, so leave the field empty there and
                // let the engine use it; a JSON file has only its file name.
                if(pickedFormat!=".bpf"&&name.Text=="")name.Text=System.IO.Path.GetFileNameWithoutExtension(file.Name);
                error.Text="";
            }catch(Exception ex){error.Text=ex.Message;}
        };
        var panel=new StackPanel { Spacing=12,MinWidth=420 };
        foreach(var element in new UIElement[]{name,mode,url,choose,fileHint,content,error})panel.Children.Add(element);
        var dialog=ProfileDialog(T("Import Profile"),panel,T("Import"));
        dialog.PrimaryButtonClick += async (_,args) => {
            var deferral=args.GetDeferral();
            try {
                // A file import may keep the name carried by the BPF container or the
                // file itself; the other modes need one typed here.
                if(mode.SelectedIndex!=2&&string.IsNullOrWhiteSpace(name.Text))throw new InvalidOperationException(T("Name Required"));
                if(mode.SelectedIndex==0) {
                    if(!Uri.TryCreate(url.Text,UriKind.Absolute,out var target)||target.Scheme is not ("http" or "https"))throw new InvalidOperationException(T("InvalidURL"));
                    await api.Call("profiles/import",HttpMethod.Post,new {name=name.Text,url=url.Text});
                } else if (mode.SelectedIndex==2) {
                    if(picked is null)throw new InvalidOperationException(T("Choose File First"));
                    var bytes = await Windows.Storage.FileIO.ReadBufferAsync(picked);
                    var data = new byte[bytes.Length];
                    using (var reader = Windows.Storage.Streams.DataReader.FromBuffer(bytes)) reader.ReadBytes(data);
                    await api.Call("profiles/import/file", HttpMethod.Post, new { name = name.Text, data = Convert.ToBase64String(data) });
                } else await api.Call("profiles/import",HttpMethod.Post,new {name=name.Text,content=content.Text});
            }catch(Exception ex){args.Cancel=true;error.Text=ex.Message;}
            finally{deferral.Complete();}
        };
        await dialog.ShowAsync();
    }
}
