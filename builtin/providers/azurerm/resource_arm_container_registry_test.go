package azurerm

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform/helper/acctest"
	"github.com/hashicorp/terraform/helper/resource"
	"github.com/hashicorp/terraform/terraform"
)

func TestAccAzureRMContainerRegistry_basic(t *testing.T) {
	ri := acctest.RandInt()
	rs := acctest.RandString(4)
	config := fmt.Sprintf(testAccAzureRMContainerRegistry_basic, ri, rs, ri)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testCheckAzureRMContainerRegistryDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testCheckAzureRMContainerRegistryExists("azurerm_container_registry.test"),
				),
			},
		},
	})
}

func TestAccAzureRMContainerRegistry_complete(t *testing.T) {
	ri := acctest.RandInt()
	rs := acctest.RandString(4)
	config := fmt.Sprintf(testAccAzureRMContainerRegistry_complete, ri, rs, ri)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testCheckAzureRMContainerRegistryDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testCheckAzureRMContainerRegistryExists("azurerm_container_registry.test"),
				),
			},
		},
	})
}

func testCheckAzureRMContainerRegistryDestroy(s *terraform.State) error {
	conn := testAccProvider.Meta().(*ArmClient).containerRegistryClient

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "azurerm_container_registry" {
			continue
		}

		name := rs.Primary.Attributes["name"]
		resourceGroup := rs.Primary.Attributes["resource_group_name"]

		resp, err := conn.GetProperties(resourceGroup, name)

		if err != nil {
			return nil
		}

		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("Container Registry still exists:\n%#v", resp)
		}
	}

	return nil
}

func testCheckAzureRMContainerRegistryExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		// Ensure we have enough information in state to look up in API
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("Not found: %s", name)
		}

		name := rs.Primary.Attributes["name"]
		resourceGroup, hasResourceGroup := rs.Primary.Attributes["resource_group_name"]
		if !hasResourceGroup {
			return fmt.Errorf("Bad: no resource group found in state for Container Registry: %s", name)
		}

		conn := testAccProvider.Meta().(*ArmClient).containerRegistryClient

		resp, err := conn.GetProperties(resourceGroup, name)
		if err != nil {
			return fmt.Errorf("Bad: Get on containerRegistryClient: %s", err)
		}

		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("Bad: Container Registry %q (resource group: %q) does not exist", name, resourceGroup)
		}

		return nil
	}
}

var testAccAzureRMContainerRegistry_basic = `
resource "azurerm_resource_group" "test" {
  name     = "testAccRg-%d"
  location = "West US"
}

resource "azurerm_storage_account" "test" {
  name                = "testaccsa%s"
  resource_group_name = "${azurerm_resource_group.test.name}"
  location            = "${azurerm_resource_group.test.location}"
  account_type        = "Standard_LRS"
}

resource "azurerm_container_registry" "test" {
  name                = "testacccr%d"
  resource_group_name = "${azurerm_resource_group.test.name}"
  location            = "${azurerm_resource_group.test.location}"

  storage_account {
    name       = "${azurerm_storage_account.test.name}"
    access_key = "${azurerm_storage_account.test.primary_access_key}"
  }
}
`

var testAccAzureRMContainerRegistry_complete = `
resource "azurerm_resource_group" "test" {
  name     = "testAccRg-%d"
  location = "West US"
}

resource "azurerm_storage_account" "test" {
  name                = "testaccsa%s"
  resource_group_name = "${azurerm_resource_group.test.name}"
  location            = "${azurerm_resource_group.test.location}"
  account_type        = "Standard_LRS"
}

resource "azurerm_container_registry" "test" {
  name                = "testacccr%d"
  resource_group_name = "${azurerm_resource_group.test.name}"
  location            = "${azurerm_resource_group.test.location}"
  admin_enabled       = false

  storage_account {
    name       = "${azurerm_storage_account.test.name}"
    access_key = "${azurerm_storage_account.test.primary_access_key}"
  }

  tags {
    environment = "production"
  }
}
`
