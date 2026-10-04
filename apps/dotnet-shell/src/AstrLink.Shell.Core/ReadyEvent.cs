using System.Text.Json.Serialization;

namespace AstrLink.Shell;

public sealed class ReadyEvent
{
    [JsonPropertyName("event")]
    public string Event { get; set; } = "";

    [JsonPropertyName("core_version")]
    public string? CoreVersion { get; set; }

    [JsonPropertyName("control_api_version")]
    public string? ControlApiVersion { get; set; }

    [JsonPropertyName("protocol_contract_version")]
    public string? ProtocolContractVersion { get; set; }

    [JsonPropertyName("inference_url")]
    public string? InferenceUrl { get; set; }

    [JsonPropertyName("control_url")]
    public string? ControlUrl { get; set; }
}

public sealed class ControlSessionFile
{
    [JsonPropertyName("schema_version")]
    public int SchemaVersion { get; set; } = 1;

    [JsonPropertyName("control_url")]
    public string? ControlUrl { get; set; }

    [JsonPropertyName("control_token")]
    public string? ControlToken { get; set; }

    [JsonPropertyName("pid")]
    public int? Pid { get; set; }

    [JsonPropertyName("control_socket")]
    public string? ControlSocket { get; set; }

    [JsonPropertyName("desktop_pid")]
    public int? DesktopPid { get; set; }
}
