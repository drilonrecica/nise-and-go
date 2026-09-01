// Generated once by nise; owned by this application. Nise will not overwrite it.

// Workbench's calls, in one place.
//
// They go through the application's one client, which supplies the versioned
// base path, the anti-forgery header on state changes, same-origin
// credentials, and cancellation. A component that called fetch directly would
// be the request that eventually goes out without one of those.
//
// Every path carries the organization, because the tenant is a path segment
// rather than something the server infers: a person can belong to more than
// one, and a request that guessed would be a request whose meaning depends on
// data the caller cannot see.

import { api } from '$lib/api/client';
import type { components } from '$lib/api/schema';

/** One piece of shared equipment. */
export type Resource = components['schemas']['Resource'];

/** What a caller may set on one. */
export type ResourceInput = components['schemas']['ResourceInput'];

/** A page of resources, with the cursor to continue with. */
export type ResourcePage = components['schemas']['ResourceCursorCollection'];

/** One booking. */
export type Reservation = components['schemas']['Reservation'];

/** A new booking. */
export type ReservationInput = components['schemas']['ReservationInput'];

/** A page of reservations. */
export type ReservationPage = components['schemas']['ReservationCursorCollection'];

/** Where a reservation is in its life. */
export type ReservationState = components['schemas']['ReservationState'];

/**
 * The five states, in the order the state machine moves through them.
 *
 * Written out rather than derived from the type, because the order is a
 * presentation decision — a filter's checkboxes should read the way the life
 * of a reservation reads — and a type gives no order at all.
 */
export const reservationStates: readonly ReservationState[] = [
	'booked',
	'checked_out',
	'returned',
	'cancelled',
	'no_show'
] as const;

/** How each state is written for a person. */
export const reservationStateLabels: Record<ReservationState, string> = {
	booked: 'Booked',
	checked_out: 'Checked out',
	returned: 'Returned',
	cancelled: 'Cancelled',
	no_show: 'Not collected'
};

/** Read one page of resources. */
export function listResources(
	orgId: string,
	query: Record<string, string | number | undefined>,
	signal?: AbortSignal
): Promise<ResourcePage> {
	return api.get('/orgs/{orgId}/resources', { params: { orgId }, query, signal } as never);
}

/** Read one resource. */
export function getResource(orgId: string, resourceId: string, signal?: AbortSignal): Promise<Resource> {
	return api.get('/orgs/{orgId}/resources/{resourceId}', {
		params: { orgId, resourceId },
		signal
	} as never);
}

/** Add one. */
export function createResource(orgId: string, input: ResourceInput): Promise<Resource> {
	return api.post('/orgs/{orgId}/resources', { params: { orgId }, body: input } as never);
}

/** Change its details. */
export function updateResource(
	orgId: string,
	resourceId: string,
	input: ResourceInput
): Promise<Resource> {
	return api.put('/orgs/{orgId}/resources/{resourceId}', {
		params: { orgId, resourceId },
		body: input
	} as never);
}

/** Take it out of service. A command, not a field. */
export function retireResource(orgId: string, resourceId: string): Promise<Resource> {
	return api.post('/orgs/{orgId}/resources/{resourceId}/retire', {
		params: { orgId, resourceId }
	} as never);
}

/** Read one page of reservations. */
export function listReservations(
	orgId: string,
	query: Record<string, string | number | undefined>,
	signal?: AbortSignal
): Promise<ReservationPage> {
	return api.get('/orgs/{orgId}/reservations', { params: { orgId }, query, signal } as never);
}

/** Book a resource for a window. */
export function createReservation(orgId: string, input: ReservationInput): Promise<Reservation> {
	return api.post('/orgs/{orgId}/reservations', { params: { orgId }, body: input } as never);
}

/** Record that the resource has been collected. */
export function checkOutReservation(orgId: string, reservationId: string): Promise<Reservation> {
	return api.post('/orgs/{orgId}/reservations/{reservationId}/check-out', {
		params: { orgId, reservationId }
	} as never);
}

/** Record that it is back. */
export function returnReservation(
	orgId: string,
	reservationId: string,
	note?: string
): Promise<Reservation> {
	return api.post('/orgs/{orgId}/reservations/{reservationId}/return', {
		params: { orgId, reservationId },
		body: note ? { note } : {}
	} as never);
}

/** Give up a booking that has not been collected. */
export function cancelReservation(orgId: string, reservationId: string): Promise<Reservation> {
	return api.post('/orgs/{orgId}/reservations/{reservationId}/cancel', {
		params: { orgId, reservationId }
	} as never);
}
