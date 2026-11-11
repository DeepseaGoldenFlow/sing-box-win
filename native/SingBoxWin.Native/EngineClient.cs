using System.Diagnostics;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json.Nodes;
namespace SingBoxWin.Native;
public sealed class EngineClient
{
    // Settings, logs and profiles stay under %APPDATA%. The lifecycle record and
    // token live in the ACL-protected %LOCALAPPDATA%\SFW\run\<instance-id>
    // directory, so they never share a folder with writable UI data.
    public static readonly string DataDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "sing-box-win");
    public static readonly string RunRoot = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "SFW", "run");
    private readonly HttpClient http = new(new HttpClientHandler { UseProxy = false, AllowAutoRedirect = false }) { Timeout = TimeSpan.FromSeconds(50) };
    private Uri? endpoint;
    private string? token;
    private string? instanceId;
    private int enginePid;
    private string? engineStart;

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

    // Reads the newest runtime.json, then the token from the same protected
    // directory. A missing or malformed token fails the probe instead of falling
    // back to an unauthenticated request (proposal §0.2.3, §0.2.4).
    private bool ReadRuntime(out Uri? uri, out string? runtimeToken, out string? runtimeInstance, out int pid, out string? start)
    {
        uri = null; runtimeToken = null; runtimeInstance = null; pid = 0; start = null;
        if (!Directory.Exists(RunRoot)) return false;
        foreach (var dir in Directory.GetDirectories(RunRoot).OrderByDescending(Directory.GetLastWriteTimeUtc))
        {
            var runtimePath = Path.Combine(dir, "runtime.json");
            var tokenPath = Path.Combine(dir, "engine.token");
            try
            {
                if (!File.Exists(runtimePath) || !File.Exists(tokenPath)) continue;
                if (IsReparse(runtimePath) || IsReparse(tokenPath)) continue;
                var runtime = JsonNode.Parse(File.ReadAllText(runtimePath));
                if ((runtime?["protocol_version"]?.GetValue<int>() ?? 0) != 1) continue;
                var candidatePid = runtime?["pid"]?.GetValue<int>() ?? 0;
                var candidateInstance = runtime?["instance_id"]?.GetValue<string>();
                var candidateStart = runtime?["process_start_time"]?.GetValue<string>();
                if (candidatePid <= 0 || string.IsNullOrEmpty(candidateInstance)) continue;
                if (!Uri.TryCreate(runtime?["url"]?.GetValue<string>(), UriKind.Absolute, out var candidate) ||
                    candidate.Scheme != "http" || candidate.Host != "127.0.0.1" || candidate.AbsolutePath != "/" || candidate.UserInfo != "") continue;
                var candidateToken = File.ReadAllText(tokenPath).Trim();
                if (candidateToken.Length < 32 || candidateToken.Any(c => !(char.IsAsciiLetterOrDigit(c) || c == '-' || c == '_'))) continue;
                using var process = Process.GetProcessById(candidatePid);
                if (!string.IsNullOrEmpty(candidateStart) && process.StartTime.ToUniversalTime().ToString("O") != candidateStart) continue;
                uri = candidate; runtimeToken = candidateToken; runtimeInstance = candidateInstance; pid = candidatePid; start = candidateStart;
                return true;
            }
            catch { continue; }
        }
        return false;
    }

    private static bool IsReparse(string path) => (File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0;

    private async Task<bool> ProbeAsync(DateTime? deadline = null)
    {
        try {
            if (!ReadRuntime(out var uri, out var runtimeToken, out var runtimeInstance, out var pid, out var start)) return false;
            double budget = deadline is null ? 600 : Math.Min(600, (deadline.Value - DateTime.UtcNow).TotalMilliseconds);
            if (budget <= 0) return false;
            using var timeout = new CancellationTokenSource(TimeSpan.FromMilliseconds(budget));
            using var request = new HttpRequestMessage(HttpMethod.Get, new Uri(uri!, "api/state"));
            request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + runtimeToken);
            using var result = await http.SendAsync(request, timeout.Token);
            if (!result.IsSuccessStatusCode) return false;
            var state = JsonNode.Parse(await result.Content.ReadAsStringAsync(timeout.Token));
            if (state?["running"] is null) return false;
            endpoint = uri; token = runtimeToken; instanceId = runtimeInstance; enginePid = pid; engineStart = start; return true;
        } catch { return false; }
    }

    private HttpRequestMessage Request(HttpMethod method, string path)
    {
        if (endpoint is null || token is null) throw new InvalidOperationException("Engine is offline.");
        var request = new HttpRequestMessage(method, new Uri(endpoint, "api/" + path));
        request.Headers.TryAddWithoutValidation("Authorization", "Bearer " + token);
        // Activation-style requests also name the instance; ordinary calls do
        // not need it, and the engine only requires it for /api/activate.
        if (instanceId is not null) request.Headers.TryAddWithoutValidation("X-SFW-Instance", instanceId);
        return request;
    }

    public async Task<JsonNode?> Call(string path, HttpMethod? method = null, object? body = null, bool missingAllowed = false)
    {
        using var request = Request(method ?? HttpMethod.Get, path);
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
        // SendAsync keeps the Authorization header; the GetAsync(url) overload
        // cannot, and an unauthenticated export would only earn a 401.
        var response = await http.SendAsync(Request(HttpMethod.Get, path), HttpCompletionOption.ResponseHeadersRead);
        if (response.StatusCode == HttpStatusCode.NotFound) { response.Dispose(); throw new FileNotFoundException("Profile not found."); }
        return response;
    }
}
