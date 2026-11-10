namespace SingBoxWin.Native;

// NEVPNStatus semantics, with Windows engine availability kept separate.
public enum ConnectionPhase { Invalid, Disconnected, Connecting, Connected, Disconnecting, Reasserting }
public readonly record struct ConnectionState(ConnectionPhase Phase, bool EngineAvailable, bool HasProfile)
{
    public bool IsEnabled => EngineAvailable && HasProfile && Phase is ConnectionPhase.Disconnected or ConnectionPhase.Connecting or ConnectionPhase.Connected or ConnectionPhase.Reasserting;
    public bool IsSwitchable => EngineAvailable && Phase is ConnectionPhase.Disconnected or ConnectionPhase.Connected;
    public bool IsConnected => Phase is ConnectionPhase.Connecting or ConnectionPhase.Connected or ConnectionPhase.Disconnecting or ConnectionPhase.Reasserting;
    public bool IsConnectedStrict => Phase is ConnectionPhase.Connected or ConnectionPhase.Reasserting;
    public bool IsGracePeriodOrEmpty => !EngineAvailable || !HasProfile || Phase is ConnectionPhase.Invalid or ConnectionPhase.Connecting or ConnectionPhase.Disconnecting or ConnectionPhase.Reasserting;
}
