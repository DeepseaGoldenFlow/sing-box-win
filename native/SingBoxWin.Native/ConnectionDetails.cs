using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using System.Text.Json.Nodes;
namespace SingBoxWin.Native;
public sealed partial class MainWindow
{
    private async Task ShowConnectionDetails(JsonObject connection)
    {
        var m=connection["metadata"];
        var panel=new StackPanel {Spacing=10,MinWidth=450};
        string chain=connection["chains"] is JsonArray chains ? string.Join(" → ",chains.Select(x=>x?.ToString())) : "";
        var fields=new (string key,string value)[] {
            ("State",T(B(connection,"closed")?"Closed":"Active")),("Created",Date(S(connection,"start"))),("Closed At",Date(S(connection,"end"))),
            ("Upload",Bytes(N(connection,"upload"))),("Download",Bytes(N(connection,"download"))),
            ("Network",S(m,"network")),("Inbound",S(m,"inboundName")+" · "+S(m,"type")),
            ("Source",S(m,"sourceIP")+":"+S(m,"sourcePort")),("Destination",S(m,"destinationIP")+":"+S(m,"destinationPort")),
            ("Domain",S(m,"host")),("Process",S(m,"processPath")+" "+S(m,"process")),
            ("Rule",S(connection,"rule")+" "+S(connection,"rulePayload")),("Chain",chain)
        };
        foreach(var field in fields) {var line=Text(T(field.key)+": "+(string.IsNullOrWhiteSpace(field.value)?"—":field.value));line.IsTextSelectionEnabled=true;panel.Children.Add(line);}
        var error=Text("",12,"#FF453A");panel.Children.Add(error);
        var dialog=ProfileDialog(T("Connection Details"),new ScrollViewer {Content=panel,MaxHeight=450},B(connection,"closed")?null:T("Close Connection"));
        dialog.DefaultButton=ContentDialogButton.Close;
        dialog.PrimaryButtonClick+=async(_,args)=>{
            var deferral=args.GetDeferral();
            try {await api.Call("connections/"+Uri.EscapeDataString(S(connection,"id")),HttpMethod.Delete);}
            catch(Exception ex){args.Cancel=true;error.Text=ex.Message;}
            finally{deferral.Complete();}
        };
        await dialog.ShowAsync();
    }
}
