using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private ComboBox? routingModePicker;
    private bool multipleRoutingModes,updatingMode;
    private string modeListSignature="";
    private void RenderModeCard(StackPanel body) {
        modeListSignature="";
        routingModePicker=new ComboBox {Header=T("Routing Mode"),HorizontalAlignment=HorizontalAlignment.Stretch};
        routingModePicker.SelectionChanged+=async(_,_)=>{
            if(updatingMode||routingModePicker.SelectedItem is not ComboBoxItem item)return;
            await Run(async()=>{await api.Call("routing",HttpMethod.Post,new{mode=(string)item.Tag});});
            await Refresh();
        };
        AddDashboardCard(body,"Routing Mode",routingModePicker);
    }
    private async Task RefreshModeCard(){
        if(routingModePicker is null)return;
        var info=Session.IsConnectedStrict?await api.Call("routing"):null;
        if(page!="Dashboard")return;
        var modes=info?["available_modes"] as JsonArray??new();
        multipleRoutingModes=modes.Count>1;
        updatingMode=true;
        string signature=modes.ToJsonString();
        if(signature!=modeListSignature){
            modeListSignature=signature;routingModePicker.Items.Clear();
            foreach(var mode in modes){
                string name=mode?.ToString()??"";
                string key=name.ToLowerInvariant() switch{"rule"=>"Rule Mode","global"=>"Global Mode","direct"=>"Direct Mode",_=>name};
                routingModePicker.Items.Add(new ComboBoxItem {Content=T(key),Tag=name});
            }
        }
        routingModePicker.SelectedItem=routingModePicker.Items.OfType<ComboBoxItem>().FirstOrDefault(x=>string.Equals((string)x.Tag,S(info,"mode"),StringComparison.OrdinalIgnoreCase));
        updatingMode=false;
    }
}
