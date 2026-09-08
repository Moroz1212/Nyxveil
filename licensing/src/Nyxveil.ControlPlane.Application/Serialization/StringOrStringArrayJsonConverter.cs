using System.Text.Json;
using System.Text.Json.Serialization;

namespace Nyxveil.ControlPlane.Application.Serialization;

/// <summary>Accepts a JSON string or string array and normalizes to a comma-separated string.</summary>
public sealed class StringOrStringArrayJsonConverter : JsonConverter<string?>
{
    public override string? Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        switch (reader.TokenType)
        {
            case JsonTokenType.Null:
                return null;
            case JsonTokenType.String:
                return reader.GetString();
            case JsonTokenType.StartArray:
            {
                var parts = new List<string>();
                while (reader.Read())
                {
                    if (reader.TokenType == JsonTokenType.EndArray)
                        break;
                    if (reader.TokenType == JsonTokenType.String)
                    {
                        var s = reader.GetString();
                        if (!string.IsNullOrWhiteSpace(s))
                            parts.Add(s.Trim());
                    }
                    else
                    {
                        throw new JsonException("management_capabilities array items must be strings");
                    }
                }

                return parts.Count == 0 ? null : string.Join(',', parts);
            }
            default:
                throw new JsonException("management_capabilities must be a string or string array");
        }
    }

    public override void Write(Utf8JsonWriter writer, string? value, JsonSerializerOptions options)
    {
        if (value is null)
            writer.WriteNullValue();
        else
            writer.WriteStringValue(value);
    }
}
