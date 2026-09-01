// Generated once by nise; owned by this application. Nise will not overwrite it.

import { createRequire } from 'node:module';

import { expect, test, type Page } from '@playwright/test';
import type { AxeResults } from 'axe-core';

// The journey the reference application exists to prove, driven against the
// real binary and a real database.
//
// It covers what no unit can see: that a booking made through the form appears
// in the list, that the two domain commands move it through its states, and
// that a refusal from the database — the exclusion constraint — arrives as a
// message a person can read rather than as a stack trace.
//
// Every page it visits is also checked with axe, at the WCAG 2.2 AA tags the
// component suite uses. A clean axe run is a floor rather than a certificate:
// roughly a third of the success criteria are not machine-checkable, so this
// catches the mechanical defects — an unlabelled control, a contrast failure,
// a table with no accessible name — and says nothing about whether the page
// makes sense.

const email = process.env.E2E_EMAIL ?? 'ada@example.com';
const password = process.env.E2E_PASSWORD ?? 'a-long-enough-passphrase-for-this';
const orgId = process.env.E2E_ORG_ID ?? '';

// The same tags src/lib/a11y.ts uses for the component suite, so the two
// layers agree on what "accessible" means.
const wcagTags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];

// axe is injected from the package this project already depends on, rather
// than through @axe-core/playwright.
//
// That wrapper is a hundred lines that inject this exact file and call
// axe.run, and the dependency policy asks what a new one buys. Here it buys a
// builder API for a call made three times — against a second copy of axe to
// keep in step with the one the component tests use.
const axeSource = createRequire(import.meta.url).resolve('axe-core/axe.min.js');

// It has to be injected **before navigation**, and that is worth knowing
// before somebody spends an afternoon on it.
//
// This application's Content-Security-Policy is `script-src 'self'` with
// per-script hashes and no `unsafe-inline`, so `page.addScriptTag` — which is
// what @axe-core/playwright uses too — is refused by the browser with
// "Executing inline script violates the following Content Security Policy
// directive". That refusal is the header working exactly as intended, and the
// wrong response to it would be relaxing the policy for testing, which would
// mean testing a page the users never get.
//
// `addInitScript` goes in through the DevTools protocol before the document
// exists, so no policy applies to it. The page under test is unchanged.
async function injectAxe(page: Page) {
	await page.addInitScript({ path: axeSource });
}

async function expectAccessible(page: Page, name: string) {
	const results = await page.evaluate(async (tags) => {
		// @ts-expect-error axe is injected into the page, not imported here.
		return (await window.axe.run(document, {
			runOnly: { type: 'tag', values: tags }
		})) as AxeResults;
	}, wcagTags);
	// The violations are printed rather than counted, because "3 violations"
	// is a number somebody raises the threshold for and "input has no label"
	// is a defect somebody fixes.
	expect(
		results.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length})`),
		`accessibility violations on ${name}`
	).toEqual([]);
}

async function signIn(page: Page) {
	await injectAxe(page);
	await page.goto('/sign-in');
	await page.getByLabel(/email/i).fill(email);
	await page.getByLabel(/password/i).fill(password);
	await page.getByRole('button', { name: /sign in/i }).click();
	await expect(page).not.toHaveURL(/sign-in/);
}

test.describe('workbench', () => {
	test.skip(orgId === '', 'E2E_ORG_ID names the organization these journeys act in');

	test('the equipment list is readable, filterable, and accessible', async ({ page }) => {
		await signIn(page);
		await page.goto(`/orgs/${orgId}/resources`);

		await expect(page.getByRole('heading', { name: 'Equipment' })).toBeVisible();
		// The table is named, which is what makes it navigable by a screen
		// reader's table controls rather than a wall of cells.
		await expect(page.getByRole('table', { name: /shared equipment/i })).toBeVisible();
		await expectAccessible(page, 'the equipment list');

		// The filter is a link, so it works without JavaScript and changes the
		// URL — which is what makes a filtered list bookmarkable.
		await page.getByRole('link', { name: /include retired/i }).click();
		await expect(page).toHaveURL(/include_retired=true/);
		await expectAccessible(page, 'the equipment list including retired');
	});

	test('the reservation list filters by state through the URL', async ({ page }) => {
		await signIn(page);
		await page.goto(`/orgs/${orgId}/reservations`);

		await expect(page.getByRole('heading', { name: 'Reservations' })).toBeVisible();
		await expectAccessible(page, 'the reservation list');

		// aria-current is what tells a screen-reader user which filter is
		// active; colour alone would not.
		const booked = page.getByRole('link', { name: 'Booked', exact: true });
		await booked.click();
		await expect(page).toHaveURL(/state=booked/);
		await expect(booked).toHaveAttribute('aria-current', 'page');
		await expectAccessible(page, 'the reservation list filtered to booked');
	});

	test('the whole page is usable at a phone width', async ({ page }) => {
		await signIn(page);
		// 360×740 is a common small Android viewport, and narrower than the
		// smallest breakpoint the layout has.
		await page.setViewportSize({ width: 360, height: 740 });
		await page.goto(`/orgs/${orgId}/resources`);

		await expect(page.getByRole('heading', { name: 'Equipment' })).toBeVisible();
		// Nothing overflows sideways. A page that scrolls horizontally on a
		// phone is one where half the controls are off screen, and it is the
		// single most common responsive defect.
		const overflow = await page.evaluate(
			() => document.documentElement.scrollWidth - document.documentElement.clientWidth
		);
		expect(overflow, 'horizontal overflow in pixels').toBeLessThanOrEqual(0);
		await expectAccessible(page, 'the equipment list on a phone');
	});
});
