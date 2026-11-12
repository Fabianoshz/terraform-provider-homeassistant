package provider

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Fabianoshz/terraform-provider-homeassistant/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &EntityResource{}
	_ resource.ResourceWithValidateConfig = &EntityResource{}
)

type EntityResource struct {
	client *client.Client
}

func NewEntityResource() resource.Resource {
	return &EntityResource{}
}

type EntityResourceModel struct {
	ID        types.String `tfsdk:"id"`
	DeviceID  types.String `tfsdk:"device_id"`
	EntityID  types.String `tfsdk:"entity_id"`
	Name      types.String `tfsdk:"name"`
	Icon      types.String `tfsdk:"icon"`
	AreaID    types.String `tfsdk:"area_id"`
	Enabled   types.Bool   `tfsdk:"enabled"`
	Visible   types.Bool   `tfsdk:"visible"`
	Aliases   types.Set    `tfsdk:"aliases"`
	Labels    types.Set    `tfsdk:"labels"`
	ExposedTo types.Set    `tfsdk:"exposed_to"`
	Value     types.String `tfsdk:"value"`
}

func (r *EntityResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_entity"
}

func (r *EntityResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages user-configurable properties of an existing Home Assistant entity.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"device_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "The ID of the device this entity belongs to. Omit for entities with no device (scripts, automations, helpers). When set, the entity is validated to belong to it.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"entity_id": schema.StringAttribute{
				Required:    true,
				Description: "The Home Assistant entity ID (e.g. sensor.living_room_temperature).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "User-defined name override for the entity. Set to \"\" to clear.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"icon": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Custom MDI icon (e.g. \"mdi:thermometer\"). Set to \"\" to clear.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"area_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Area to assign this entity to. Set to \"\" to clear.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether the entity is enabled. When false, sets disabled_by to \"user\".",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"visible": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Whether the entity is visible. When false, sets hidden_by to \"user\".",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"aliases": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "Voice-assistant aliases (alternative names) for the entity.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
			"labels": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Description: "Label IDs assigned to the entity.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
			"exposed_to": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Assistants the entity is exposed to (e.g. \"conversation\", \"cloud.alexa\", \"cloud.google_assistant\"). When set, manages the complete exposure set for the entity.",
			},
			"value": schema.StringAttribute{
				Optional:    true,
				Description: "Desired state of a settable entity: number/input_number/text/input_text (set_value) or select/input_select (select_option). Numbers are compared numerically. Removing it stops managing the value without resetting it.",
			},
		},
	}
}

func (r *EntityResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg EntityResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Value.IsNull() || cfg.EntityID.IsUnknown() {
		return
	}
	domain, _, _, ok := valueService(cfg.EntityID.ValueString())
	if !ok {
		resp.Diagnostics.AddAttributeError(path.Root("value"), "Unsupported entity domain",
			fmt.Sprintf("value can't be set on %q entities; supported: number, input_number, text, input_text, select, input_select.", domain))
	}
}

func (r *EntityResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *EntityResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan EntityResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	existing, err := r.client.GetEntityRegistryEntry(ctx, plan.EntityID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read entity", err.Error())
		return
	}
	if existing == nil {
		resp.Diagnostics.AddError("Entity not found", fmt.Sprintf("Entity %q does not exist in the registry.", plan.EntityID.ValueString()))
		return
	}
	// Only enforce the device binding when a device_id is being managed.
	// Deviceless entities (scripts, automations, helpers) omit it.
	if !plan.DeviceID.IsNull() && !plan.DeviceID.IsUnknown() && plan.DeviceID.ValueString() != "" {
		if existing.DeviceID == nil || *existing.DeviceID != plan.DeviceID.ValueString() {
			resp.Diagnostics.AddError("Entity does not belong to device", fmt.Sprintf("Entity %q does not belong to device %q.", plan.EntityID.ValueString(), plan.DeviceID.ValueString()))
			return
		}
	}

	update := r.toUpdate(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	entry, err := r.client.UpdateEntity(ctx, update)
	if err != nil {
		resp.Diagnostics.AddError("Failed to configure entity", err.Error())
		return
	}

	r.applyExposure(ctx, plan.EntityID.ValueString(), plan.ExposedTo, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.applyValue(ctx, plan.EntityID.ValueString(), plan.Value, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	model := r.toModel(ctx, entry, &resp.Diagnostics)
	model.ExposedTo = plan.ExposedTo
	model.Value = plan.Value
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *EntityResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state EntityResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	entry, err := r.client.GetEntityRegistryEntry(ctx, state.EntityID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Failed to read entity", err.Error())
		return
	}
	if entry == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	// Enforce the device binding only when a device_id is tracked in state.
	if !state.DeviceID.IsNull() && (entry.DeviceID == nil || *entry.DeviceID != state.DeviceID.ValueString()) {
		resp.State.RemoveResource(ctx)
		return
	}

	model := r.toModel(ctx, entry, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Exposure lives in a separate registry; only track it when the field is managed.
	if !state.ExposedTo.IsNull() {
		current, err := r.client.GetExposedAssistants(ctx, state.EntityID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Failed to read entity exposure", err.Error())
			return
		}
		if current == nil {
			current = []string{}
		}
		set, d := types.SetValueFrom(ctx, types.StringType, current)
		resp.Diagnostics.Append(d...)
		model.ExposedTo = set
	}

	// Like exposure, the value is only tracked when managed.
	if !state.Value.IsNull() {
		model.Value = state.Value
		current, err := r.client.GetState(ctx, state.EntityID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Failed to read entity state", err.Error())
			return
		}
		// An offline device reports no value; keep the last known one rather than show drift.
		if current != nil && current.State != "unavailable" && current.State != "unknown" &&
			!valuesEqual(state.EntityID.ValueString(), state.Value.ValueString(), current.State) {
			model.Value = types.StringValue(current.State)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *EntityResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan EntityResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	update := r.toUpdate(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	entry, err := r.client.UpdateEntity(ctx, update)
	if err != nil {
		resp.Diagnostics.AddError("Failed to update entity", err.Error())
		return
	}

	r.applyExposure(ctx, plan.EntityID.ValueString(), plan.ExposedTo, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	r.applyValue(ctx, plan.EntityID.ValueString(), plan.Value, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	model := r.toModel(ctx, entry, &resp.Diagnostics)
	model.ExposedTo = plan.ExposedTo
	model.Value = plan.Value
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *EntityResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state EntityResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Clear user-managed fields; entities cannot be deleted via API.
	_, err := r.client.UpdateEntity(ctx, client.EntityUpdate{
		EntityID:      state.EntityID.ValueString(),
		SetName:       true,
		SetIcon:       true,
		SetAreaID:     true,
		SetDisabledBy: true,
		SetHiddenBy:   true,
		SetAliases:    true,
		SetLabels:     true,
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to reset entity on destroy", err.Error())
	}

	if !state.ExposedTo.IsNull() {
		if current, err := r.client.GetExposedAssistants(ctx, state.EntityID.ValueString()); err == nil && len(current) > 0 {
			_ = r.client.SetEntityExposure(ctx, state.EntityID.ValueString(), current, false)
		}
	}
}

func (r *EntityResource) toUpdate(ctx context.Context, m EntityResourceModel, diags *diag.Diagnostics) client.EntityUpdate {
	update := client.EntityUpdate{EntityID: m.EntityID.ValueString()}
	if !m.Name.IsNull() && !m.Name.IsUnknown() {
		update.SetName = true
		if m.Name.ValueString() != "" {
			update.Name = strPtr(m.Name.ValueString())
		}
	}
	if !m.Icon.IsNull() && !m.Icon.IsUnknown() {
		update.SetIcon = true
		if m.Icon.ValueString() != "" {
			update.Icon = strPtr(m.Icon.ValueString())
		}
	}
	if !m.AreaID.IsNull() && !m.AreaID.IsUnknown() {
		update.SetAreaID = true
		if m.AreaID.ValueString() != "" {
			update.AreaID = strPtr(m.AreaID.ValueString())
		}
	}
	if !m.Enabled.IsNull() && !m.Enabled.IsUnknown() {
		update.SetDisabledBy = true
		if !m.Enabled.ValueBool() {
			update.DisabledBy = strPtr("user")
		}
	}
	if !m.Visible.IsNull() && !m.Visible.IsUnknown() {
		update.SetHiddenBy = true
		if !m.Visible.ValueBool() {
			update.HiddenBy = strPtr("user")
		}
	}
	if !m.Aliases.IsNull() && !m.Aliases.IsUnknown() {
		update.SetAliases = true
		diags.Append(m.Aliases.ElementsAs(ctx, &update.Aliases, false)...)
	}
	if !m.Labels.IsNull() && !m.Labels.IsUnknown() {
		update.SetLabels = true
		diags.Append(m.Labels.ElementsAs(ctx, &update.Labels, false)...)
	}
	return update
}

// valueService returns the service (and its data key) that sets the value of an entity in this domain.
func valueService(entityID string) (domain, service, key string, ok bool) {
	domain, _, _ = strings.Cut(entityID, ".")
	switch domain {
	case "number", "input_number", "text", "input_text":
		return domain, "set_value", "value", true
	case "select", "input_select":
		return domain, "select_option", "option", true
	}
	return domain, "", "", false
}

// valuesEqual compares numeric domains by value so "-2.6" matches "-2.60" and "0" matches "0.0".
func valuesEqual(entityID, a, b string) bool {
	if domain, _, _ := strings.Cut(entityID, "."); domain == "number" || domain == "input_number" {
		fa, errA := strconv.ParseFloat(a, 64)
		fb, errB := strconv.ParseFloat(b, 64)
		if errA == nil && errB == nil {
			return math.Abs(fa-fb) < 1e-9
		}
	}
	return a == b
}

// applyValue sets the entity's state through its domain service when it differs from the desired value.
func (r *EntityResource) applyValue(ctx context.Context, entityID string, desired types.String, diags *diag.Diagnostics) {
	if desired.IsNull() || desired.IsUnknown() {
		return
	}
	domain, service, key, ok := valueService(entityID)
	if !ok {
		diags.AddError("Unsupported entity domain", fmt.Sprintf("value can't be set on %q entities.", domain))
		return
	}
	current, err := r.client.GetState(ctx, entityID)
	if err != nil {
		diags.AddError("Failed to read entity state", err.Error())
		return
	}
	if current == nil || current.State == "unavailable" {
		diags.AddError("Entity unavailable", fmt.Sprintf("Can't set the value of %q while it is unavailable.", entityID))
		return
	}
	if valuesEqual(entityID, desired.ValueString(), current.State) {
		return
	}
	if err := r.client.CallService(ctx, domain, service, map[string]any{"entity_id": entityID, key: desired.ValueString()}); err != nil {
		diags.AddError("Failed to set entity value", err.Error())
	}
}

// applyExposure makes the entity's exposure match the desired assistant set exactly.
func (r *EntityResource) applyExposure(ctx context.Context, entityID string, desired types.Set, diags *diag.Diagnostics) {
	if desired.IsNull() || desired.IsUnknown() {
		return
	}
	var want []string
	diags.Append(desired.ElementsAs(ctx, &want, false)...)
	if diags.HasError() {
		return
	}
	current, err := r.client.GetExposedAssistants(ctx, entityID)
	if err != nil {
		diags.AddError("Failed to read entity exposure", err.Error())
		return
	}
	wantSet := map[string]bool{}
	for _, a := range want {
		wantSet[a] = true
	}
	curSet := map[string]bool{}
	for _, a := range current {
		curSet[a] = true
	}
	var toExpose, toUnexpose []string
	for _, a := range want {
		if !curSet[a] {
			toExpose = append(toExpose, a)
		}
	}
	for _, a := range current {
		if !wantSet[a] {
			toUnexpose = append(toUnexpose, a)
		}
	}
	if len(toExpose) > 0 {
		if err := r.client.SetEntityExposure(ctx, entityID, toExpose, true); err != nil {
			diags.AddError("Failed to expose entity", err.Error())
			return
		}
	}
	if len(toUnexpose) > 0 {
		if err := r.client.SetEntityExposure(ctx, entityID, toUnexpose, false); err != nil {
			diags.AddError("Failed to unexpose entity", err.Error())
			return
		}
	}
}

func (r *EntityResource) toModel(ctx context.Context, e *client.EntityRegistryEntry, diags *diag.Diagnostics) EntityResourceModel {
	return EntityResourceModel{
		ID:        types.StringValue(e.EntityID),
		DeviceID:  types.StringPointerValue(e.DeviceID),
		EntityID:  types.StringValue(e.EntityID),
		Name:      stringPtrValue(e.Name),
		Icon:      stringPtrValue(e.Icon),
		AreaID:    stringPtrValue(e.AreaID),
		Enabled:   types.BoolValue(e.DisabledBy == nil),
		Visible:   types.BoolValue(e.HiddenBy == nil),
		Aliases:   strsToSet(ctx, e.Aliases, diags),
		Labels:    strsToSet(ctx, e.Labels, diags),
		ExposedTo: types.SetNull(types.StringType),
		Value:     types.StringNull(),
	}
}

// strsToSet converts a string slice to a set, dropping blank entries and
// returning a null set when nothing remains. HA returns [] or [""] for "no
// aliases/labels", which must normalize to null to match an unset config.
func strsToSet(ctx context.Context, in []string, diags *diag.Diagnostics) types.Set {
	vals := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return types.SetNull(types.StringType)
	}
	set, d := types.SetValueFrom(ctx, types.StringType, vals)
	diags.Append(d...)
	return set
}
