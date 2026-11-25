package protocol

// Names of the configuration and export formats.
const (
	// FormatCSV is the name of the CSV format.
	FormatCSV = "csv"
	// FormatJSON is the name of the JSON format.
	FormatJSON = "json"
	// FormatTOML is the name of the TOML format.
	FormatTOML = "toml"
	// FormatYAML is the name of the YAML format.
	FormatYAML = "yaml"
)

// NetworkTCP is the name of the TCP network, as accepted by [net.Listen] and [net.Dial].
const NetworkTCP = "tcp"
