using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Windows.Storage.Pickers;
namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private async Task ImportProfileDialog()
    {
        var name = new TextBox { Header = T("Name") };
        var mode = new ComboBox { Header = T("Import Method"), ItemsSource = new[] { T("Subscription URL"), T("Paste JSON"), T("JSON File") }, SelectedIndex = 0, HorizontalAlignment = HorizontalAlignment.Stretch };
        var url = new TextBox { Header = T("URL"), PlaceholderText = "https://" };
        var content = new TextBox { Header = T("Configuration JSON"), AcceptsReturn = true, Height = 220, TextWrapping = TextWrapping.NoWrap, Visibility = Visibility.Collapsed };
        var choose = new Button { Content = T("Choose File"), Visibility = Visibility.Collapsed };
        var error = Text("",12,"#FF453A");
        mode.SelectionChanged += (_,_) => {
            url.Visibility = mode.SelectedIndex == 0 ? Visibility.Visible : Visibility.Collapsed;
            content.Visibility = mode.SelectedIndex == 0 ? Visibility.Collapsed : Visibility.Visible;
            content.IsReadOnly = mode.SelectedIndex == 2;
            choose.Visibility = mode.SelectedIndex == 2 ? Visibility.Visible : Visibility.Collapsed;
        };
        choose.Click += async (_,_) => {
            try {
                var picker=new FileOpenPicker(); picker.FileTypeFilter.Add(".json");
                WinRT.Interop.InitializeWithWindow.Initialize(picker,WinRT.Interop.WindowNative.GetWindowHandle(this));
                var file=await picker.PickSingleFileAsync();
                if(file is null)return;
                if((await file.GetBasicPropertiesAsync()).Size>16*1024*1024)throw new InvalidOperationException(T("File Too Large"));
                content.Text=await Windows.Storage.FileIO.ReadTextAsync(file);
                if(name.Text=="")name.Text=System.IO.Path.GetFileNameWithoutExtension(file.Name);
                error.Text="";
            }catch(Exception ex){error.Text=ex.Message;}
        };
        var panel=new StackPanel { Spacing=12,MinWidth=420 };
        foreach(var element in new UIElement[]{name,mode,url,choose,content,error})panel.Children.Add(element);
        var dialog=ProfileDialog(T("Import Profile"),panel,T("Import"));
        dialog.PrimaryButtonClick += async (_,args) => {
            var deferral=args.GetDeferral();
            try {
                if(string.IsNullOrWhiteSpace(name.Text))throw new InvalidOperationException(T("Name Required"));
                if(mode.SelectedIndex==0) {
                    if(!Uri.TryCreate(url.Text,UriKind.Absolute,out var target)||target.Scheme is not ("http" or "https"))throw new InvalidOperationException(T("InvalidURL"));
                    await api.Call("profiles/import",HttpMethod.Post,new {name=name.Text,url=url.Text});
                } else await api.Call("profiles/import",HttpMethod.Post,new {name=name.Text,content=content.Text});
            }catch(Exception ex){args.Cancel=true;error.Text=ex.Message;}
            finally{deferral.Complete();}
        };
        await dialog.ShowAsync();
    }
}
