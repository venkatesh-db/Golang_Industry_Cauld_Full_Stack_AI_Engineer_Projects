package ride

import "testing"

func TestValidateCreateCommand(t *testing.T) {
	valid := CreateCommand{RiderName: "Venkatesh", Destination: "Indiranagar", PickupLatitude: 12.9716, PickupLongitude: 77.5946}
	if err := validate(valid); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}

	invalid := []CreateCommand{
		{Destination: "Indiranagar", PickupLatitude: 12, PickupLongitude: 77},
		{RiderName: "Venkatesh", PickupLatitude: 12, PickupLongitude: 77},
		{RiderName: "Venkatesh", Destination: "Indiranagar", PickupLatitude: 91, PickupLongitude: 77},
		{RiderName: "Venkatesh", Destination: "Indiranagar", PickupLatitude: 12, PickupLongitude: 181},
	}
	for _, command := range invalid {
		if err := validate(command); err == nil {
			t.Fatalf("invalid command accepted: %#v", command)
		}
	}
}
