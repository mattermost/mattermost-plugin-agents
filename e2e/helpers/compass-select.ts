import { Locator } from '@playwright/test';

/** Opens a compass-ui Select or Combobox and picks the option with the given label. */
export async function chooseCompassOption(trigger: Locator, option: string | RegExp): Promise<void> {
    await trigger.click();
    await trigger.page()
        .getByRole('listbox')
        .getByRole('option', { name: option, exact: typeof option === 'string' })
        .click();
}
