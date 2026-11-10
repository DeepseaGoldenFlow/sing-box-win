using System.Diagnostics;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json.Nodes;
namespace SingBoxWin.Native;
public sealed class EngineClient
{
    public static readonly string DataDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "sing-box-win");
    private readonly HttpClient http = new(new HttpClientHandler { UseProxy = false, AllowAutoRedirect = false }) { Timeout = TimeSpan.FromSeconds(50) };
    private Uri? endpoint;
    public async Task ConnectAsync()
    {
        if (await ProbeAsync()) return;
        var candidates = new[] { Path.Combine(AppContext.BaseDirectory, "sing-box-win.exe"), Path.Combine(AppContext.BaseDirectory, "..", "sing-box-win.exe"), Path.GetFullPath("build/sing-box-win.exe") };
        string exe = candidates.FirstOrDefault(File.Exists) ?? throw new FileNotFoundException("build\\sing-box-win.exe");
        Process.Start(new ProcessStartInfo(exe, "--headless") { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = Path.GetDirectoryName(Path.GetFullPath(exe))! });
        var until = DateTime.UtcNow.AddSeconds(10);
        while (DateTime.UtcNow < until) { if (await ProbeAsync(until)) return; var remaining = until - DateTime.UtcNow; if (remaining > TimeSpan.Zero) await Task.Delay(TimeSpan.FromMilliseconds(Math.Min(200, remaining.TotalMilliseconds))); }
        throw new TimeoutException("Engine startup timed out (10 s).");
    }
    private async Task<bool> ProbeAsync(DateTime? deadline = null)
    {
        try {
            var runtime = JsonNode.Parse(await File.ReadAllTextAsync(Path.Combine(DataDir, "runtime.json")));
            if ((runtime?["pid"]?.GetValue<int>() ?? 0) <= 0) return false;
            if (!Uri.TryCreate(runtime?["url"]?.GetValue<string>(), UriKind.Absolute, out var uri) || uri.Scheme != "http" || uri.Host != "127.0.0.1" || uri.AbsolutePath != "/" || uri.UserInfo != "") return false;
            double budget = deadline is null ? 600 : Math.Min(600, (deadline.Value - DateTime.UtcNow).TotalMilliseconds);
            if (budget <= 0) return false;
            using var timeout = new CancellationTokenSource(TimeSpan.FromMilliseconds(budget));
            using var result = await http.GetAsync(new Uri(uri, "api/state"), timeout.Token);
            if (!result.IsSuccessStatusCode) return false;
            var state = JsonNode.Parse(await result.Content.ReadAsStringAsync(timeout.Token));
            if (state?["running"] is null) return false;
            endpoint = uri; return true;
        } catch { return false; }
    }
    public async Task<JsonNode?> Call(string path, HttpMethod? method = null, object? body = null, bool missingAllowed = false)
    {
        if (endpoint is null) throw new InvalidOperationException("Engine is offline.");
        using var request = new HttpRequestMessage(method ?? HttpMethod.Get, new Uri(endpoint, "api/" + path));
        if (body is not null) request.Content = JsonContent.Create(body);
        using var response = await http.SendAsync(request);
        if (missingAllowed && response.StatusCode == HttpStatusCode.NotFound) return null;
        string text = await response.Content.ReadAsStringAsync();
        if (!response.IsSuccessStatusCode) throw new InvalidOperationException(text.Trim());
        return string.IsNullOrWhiteSpace(text) ? null : JsonNode.Parse(text);
    }
    // Raw returns the response itself for binary payloads (profile export).
    // The caller owns disposal. It reuses the same client, so redirects stay
    // disabled and the engine's 127.0.0.1 origin check cannot be bypassed.
    public async Task<HttpResponseMessage> Raw(string path)
    {
        if (endpoint is null) throw new InvalidOperationException("Engine is offline.");
        var response = await http.GetAsync(new Uri(endpoint, "api/" + path), HttpCompletionOption.ResponseHeadersRead);
        if (response.StatusCode == HttpStatusCode.NotFound) { response.Dispose(); throw new FileNotFoundException("Profile not found."); }
        return response;
    }
}
