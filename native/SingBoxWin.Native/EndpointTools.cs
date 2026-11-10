using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private StackPanel? endpointPanel;
    private string endpointSignature="";
    private void RenderEndpointTools(StackPanel body) {
        endpointSignature="";
        body.Children.Add(Text(T("Live Endpoint Hint"),12,"#9A9AA0"));
        endpointPanel=new StackPanel {Spacing=16};body.Children.Add(endpointPanel);
    }
    private async Task RefreshEndpointTools() {
        var endpoints=await api.Call("tools/endpoints") as JsonArray??new();
        if(page!="Tools"||endpointPanel is null)return;
        string signature=endpoints.ToJsonString();
        if(signature==endpointSignature)return;
        endpointSignature=signature;endpointPanel.Children.Clear();
        if(endpoints.Count==0){endpointPanel.Children.Add(Text(T("No Endpoints")));return;}
        foreach(var ep in endpoints){
            string tag=S(ep,"tag"),type=S(ep,"type");
            var panel=new StackPanel {Spacing=10};
            panel.Children.Add(Text(tag+" · "+type,18));
            panel.Children.Add(Text(T("State")+": "+S(ep,"backend_state")));
            if(S(ep,"source")!="live"){panel.Children.Add(Text(T("Endpoint Not Live"),12,"#9A9AA0"));endpointPanel.Children.Add(Card(panel));continue;}
            var self=ep?["self"];
            panel.Children.Add(Text(T("This Device")+": "+S(self,"hostname")));
            panel.Children.Add(Text(self?["ips"] is JsonArray ips ?string.Join(" · ",ips.Select(x=>x?.ToString())):"—"));
            if(B(ep,"needs_login"))panel.Children.Add(Text(T("Endpoint Login Needed"),12,"#FF9F0A"));
            if(self?["key_expiry"] is not null && N(self,"key_expiry")>0)panel.Children.Add(Text(T("KeyExpiry")+": "+DateTimeOffset.FromUnixTimeSeconds((long)N(self,"key_expiry")).ToLocalTime().ToString("yyyy-MM-dd HH:mm")));
            var peers=ep?["peers"] as JsonArray??new();
            panel.Children.Add(Text(T("Peers")+" · "+peers.Count,16));
            foreach(var peer in peers){
                string addresses=peer?["ips"] is JsonArray peerIPs?string.Join(" · ",peerIPs.Select(x=>x?.ToString())):"";
                panel.Children.Add(Text((B(peer,"online")?"● ":"○ ")+S(peer,"hostname")+" · "+addresses+" · "+S(peer,"os"),13,B(peer,"online")?"#30D158":"#9A9AA0"));
            }
            var exits=new List<(string id,string name)>{("","— "+T("No Exit Node")+" —")};
            foreach(var peer in peers.Where(p=>B(p,"exit_node_option")&&!B(p,"expired")))exits.Add((S(peer,"id"),S(peer,"hostname")));
            if(exits.Count>1||ep?["exit_node"] is not null){
                var picker=new ComboBox {Header=T("Exit Node"),ItemsSource=exits.Select(x=>x.name).ToArray(),SelectedIndex=Math.Max(0,exits.FindIndex(x=>x.id==S(ep?["exit_node"],"id")))};
                panel.Children.Add(picker);
                panel.Children.Add(Button(T("Apply Exit Node"),async()=>{await api.Call("tools/tailscale/exit-node",HttpMethod.Post,new{tag,id=exits[Math.Max(0,picker.SelectedIndex)].id});endpointSignature="";}));
            }
            endpointPanel.Children.Add(Card(panel));
        }
    }
}
