package domain

import "testing"

func TestXrayBindingValidationAllowsNativeTagsAndRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	valid := XrayBinding{ID: "native_route", InboundTag: "VLESS inbound:443", OutboundTag: "proxy/de", Enabled: true}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []XrayBinding{
		{ID: "INVALID", InboundTag: "in", OutboundTag: "out"},
		{ID: "route", InboundTag: " bad", OutboundTag: "out"},
		{ID: "route", InboundTag: "in", OutboundTag: "bad\nvalue"},
	} {
		if err := binding.Validate(); err == nil {
			t.Fatalf("accepted binding %#v", binding)
		}
	}
}
