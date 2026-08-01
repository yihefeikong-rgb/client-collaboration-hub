package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type adapterDoctorOutput struct {
	RequestedClient string                `json:"requested_client"`
	Adapters        []adapterDoctorResult `json:"adapters"`
}

type adapterDoctorResult struct {
	Client       string                         `json:"client"`
	Status       string                         `json:"status"`
	Protocol     *reasonixDesktopBridgeProtocol `json:"protocol,omitempty"`
	ClientInfo   *reasonixDesktopBridgeClient   `json:"client_info,omitempty"`
	CCProtocol   *ccHahahaDesktopBridgeProtocol `json:"cc_protocol,omitempty"`
	CCClientInfo *ccHahahaDesktopBridgeClient   `json:"cc_client_info,omitempty"`
	Capabilities []string                       `json:"capabilities,omitempty"`
	Reason       string                         `json:"reason,omitempty"`
}

// IsAdapterDoctorCommand lets the executable skip storage initialization for
// this read-only diagnostic command.
func IsAdapterDoctorCommand(args []string) bool {
	_, args, err := extractJSONFlag(args)
	return err == nil && matches(args, "adapter", "doctor")
}

func (a *App) adapterDoctor(ctx context.Context, args []string, jsonOutput bool) (int, error) {
	fs := newFlagSet("adapter doctor")
	client := fs.String("client", "", "")
	if err := parse(fs, args); err != nil {
		return ExitValidation, err
	}
	if *client != "reasonix" && *client != "cc-haha" && *client != "all" {
		return ExitValidation, errUsage
	}
	adapters := make([]adapterDoctorResult, 0, 2)
	var firstErr error
	if *client == "reasonix" || *client == "all" {
		result, err := a.reasonixAdapterDoctor(ctx)
		adapters = append(adapters, result)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if *client == "cc-haha" || *client == "all" {
		result, err := a.ccHahahaAdapterDoctor(ctx)
		adapters = append(adapters, result)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	output := adapterDoctorOutput{RequestedClient: *client, Adapters: adapters}
	a.writeAdapterDoctor(jsonOutput, output)
	if firstErr != nil {
		return ExitValidation, firstErr
	}
	return ExitOK, nil
}

func (a *App) ccHahahaAdapterDoctor(ctx context.Context) (adapterDoctorResult, error) {
	discoveryPath, err := ccHahahaDesktopBridgeDiscoveryPath()
	if err != nil {
		return unavailableCCHahahaAdapterDoctor(), errors.New("CC-HAHA desktop bridge is unavailable")
	}
	return ccHahahaAdapterDoctorAt(ctx, discoveryPath, verifyCCHahaDesktopBridge)
}

func ccHahahaAdapterDoctorAt(ctx context.Context, discoveryPath string, verifyBridge func(ccHahahaDesktopBridgeDiscovery) error) (adapterDoctorResult, error) {
	result := unavailableCCHahahaAdapterDoctor()
	discovery, err := readCCHahaDesktopBridge(discoveryPath)
	if err != nil {
		result.Reason = "bridge discovery is unavailable"
		return result, errors.New("CC-HAHA desktop bridge is unavailable")
	}
	if verifyBridge == nil {
		result.Reason = "bridge process verifier is unavailable"
		return result, errors.New("CC-HAHA desktop bridge process verifier is unavailable")
	}
	if err := verifyBridge(discovery); err != nil {
		result.Status = "INCOMPATIBLE"
		result.Reason = err.Error()
		return result, err
	}
	health, err := inspectCCHahaDesktopBridge(ctx, discovery, ccHahahaWorkDefault)
	if health.Protocol.Name != "" {
		protocol := health.Protocol
		result.CCProtocol = &protocol
	}
	if health.Client.Name != "" || health.Client.Version != "" || health.Client.Build != "" {
		client := health.Client
		result.CCClientInfo = &client
	}
	if len(health.Capabilities) > 0 {
		result.Capabilities = append([]string(nil), health.Capabilities...)
		sort.Strings(result.Capabilities)
	}
	if err != nil {
		if errors.Is(err, errCCHahaDesktopBridgeIncompatible) {
			result.Status = "INCOMPATIBLE"
			result.Reason = err.Error()
			return result, err
		}
		result.Reason = "bridge health check failed"
		return result, err
	}
	result.Status = "COMPATIBLE"
	return result, nil
}

func unavailableCCHahahaAdapterDoctor() adapterDoctorResult {
	return adapterDoctorResult{Client: ccHahahaDesktopBridgeClientName, Status: "UNAVAILABLE"}
}

func (a *App) reasonixAdapterDoctor(ctx context.Context) (adapterDoctorResult, error) {
	discoveryPath, err := reasonixDesktopBridgeDiscoveryPath()
	if err != nil {
		return unavailableReasonixAdapterDoctor(), errors.New("Reasonix desktop bridge is unavailable")
	}
	return reasonixAdapterDoctorAt(ctx, discoveryPath, verifyReasonixDesktopProcess)
}

func reasonixAdapterDoctorAt(ctx context.Context, discoveryPath string, verifyProcess func(int) error) (adapterDoctorResult, error) {
	result := unavailableReasonixAdapterDoctor()
	discovery, err := readReasonixDesktopBridge(discoveryPath)
	if err != nil {
		result.Reason = "bridge discovery is unavailable"
		return result, errors.New("Reasonix desktop bridge is unavailable")
	}
	if verifyProcess == nil {
		result.Reason = "bridge process verifier is unavailable"
		return result, errors.New("Reasonix desktop bridge process verifier is unavailable")
	}
	if err := verifyProcess(discovery.PID); err != nil {
		if errors.Is(err, errReasonixDesktopBridgeIncompatible) {
			result.Status = "INCOMPATIBLE"
			result.Reason = err.Error()
			return result, err
		}
		result.Reason = "bridge process identity is invalid"
		return result, fmt.Errorf("Reasonix desktop bridge process identity is invalid: %w", err)
	}
	health, err := inspectReasonixDesktopBridge(ctx, discovery)
	if health.Protocol.Name != "" {
		protocol := health.Protocol
		result.Protocol = &protocol
	}
	if health.Client.Name != "" || health.Client.Version != "" || health.Client.Build != "" {
		client := health.Client
		result.ClientInfo = &client
	}
	if len(health.Capabilities) > 0 {
		result.Capabilities = append([]string(nil), health.Capabilities...)
		sort.Strings(result.Capabilities)
	}
	if err != nil {
		if errors.Is(err, errReasonixDesktopBridgeIncompatible) {
			result.Status = "INCOMPATIBLE"
			result.Reason = err.Error()
			return result, err
		}
		result.Reason = "bridge health check failed"
		return result, err
	}
	result.Status = "COMPATIBLE"
	return result, nil
}

func unavailableReasonixAdapterDoctor() adapterDoctorResult {
	return adapterDoctorResult{Client: reasonixDesktopBridgeClientName, Status: "UNAVAILABLE"}
}

func (a *App) writeAdapterDoctor(jsonOutput bool, output adapterDoctorOutput) {
	if jsonOutput {
		a.writeJSON(output)
		return
	}
	fmt.Fprintf(a.Stdout, "requested_client: %s\n", output.RequestedClient)
	for _, result := range output.Adapters {
		fmt.Fprintf(a.Stdout, "adapter: %s\nstatus: %s\n", result.Client, result.Status)
		if result.Protocol != nil {
			fmt.Fprintf(a.Stdout, "protocol: %s/%d.%d\n", result.Protocol.Name, result.Protocol.Major, result.Protocol.Minor)
		}
		if result.CCProtocol != nil {
			fmt.Fprintf(a.Stdout, "protocol: %s/%d.%d\n", result.CCProtocol.Name, result.CCProtocol.Major, result.CCProtocol.Minor)
		}
		if result.ClientInfo != nil {
			fmt.Fprintf(a.Stdout, "client_name: %s\nclient_version: %s\nclient_build: %s\n", result.ClientInfo.Name, result.ClientInfo.Version, result.ClientInfo.Build)
		}
		if result.CCClientInfo != nil {
			fmt.Fprintf(a.Stdout, "client_name: %s\nclient_version: %s\nclient_build: %s\n", result.CCClientInfo.Name, result.CCClientInfo.Version, result.CCClientInfo.Build)
		}
		if len(result.Capabilities) > 0 {
			fmt.Fprintf(a.Stdout, "capabilities: %s\n", strings.Join(result.Capabilities, ","))
		}
		if result.Reason != "" {
			fmt.Fprintf(a.Stdout, "reason: %s\n", result.Reason)
		}
	}
}
