<!-- Generated once by nise; owned by this application. Nise will not overwrite it. -->
<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { page } from '$app/state';

	import Badge from '$lib/components/ui/Badge.svelte';
	import Button from '$lib/components/ui/Button.svelte';
	import EmptyState from '$lib/components/ui/EmptyState.svelte';
	import Pagination from '$lib/components/ui/Pagination.svelte';
	import ProblemAlert from '$lib/components/ui/ProblemAlert.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import Table from '$lib/components/ui/Table.svelte';
	import { toaster } from '$lib/components/ui/toast.svelte';
	import {
		cancelReservation,
		checkOutReservation,
		listReservations,
		returnReservation,
		reservationStateLabels,
		reservationStates,
		type Reservation,
		type ReservationPage,
		type ReservationState
	} from '$lib/features/workbench/api';
	import { session } from '$lib/session.svelte';
	import { listHref, listQuery, readListState } from '$lib/table/list-state';

	const orgId = $derived(page.params.orgId ?? '');
	const mayManageOthers = $derived(session.can('reservations.manage'));

	const filters = ['resource_id', 'holder_id', 'state'] as const;
	const listState = $derived(readListState(page.url, filters));
	const activeState = $derived(listState.filters.state ?? '');
	const mine = $derived(listState.filters.holder_id === session.account?.id);

	let result = $state<ReservationPage | null>(null);
	let failure = $state<unknown>(null);
	// The reservation a command is running against, so only its own buttons
	// go busy. A single boolean would disable the whole table for one row's
	// request, which reads as the page having frozen.
	let running = $state<string | null>(null);

	$effect(() => {
		const query = listQuery(listState);
		const controller = new AbortController();
		result = null;
		failure = null;
		listReservations(orgId, query, controller.signal)
			.then((loaded) => (result = loaded))
			.catch((error) => {
				if (error instanceof Error && error.name === 'AbortError') return;
				failure = error;
			});
		return () => controller.abort();
	});

	const nextHref = $derived(
		result?.page.next_cursor
			? listHref(page.url, { after: result.page.next_cursor }, filters)
			: undefined
	);

	function stateHref(value: ReservationState | ''): string {
		return listHref(page.url, { filters: { state: value } }, filters);
	}

	const mineHref = $derived(
		listHref(
			page.url,
			{ filters: { holder_id: mine ? '' : (session.account?.id ?? '') } },
			filters
		)
	);

	/**
	 * Whether the signed-in person may act on this reservation.
	 *
	 * The same asymmetry the server enforces: acting on your own needs no
	 * permission, because it is yours. Showing a button the server would
	 * refuse teaches people to distrust the interface; hiding one it would
	 * allow makes them think the software is broken.
	 */
	function mayAct(reservation: Reservation): boolean {
		return mayManageOthers || reservation.holder_id === session.account?.id;
	}

	async function run(
		reservation: Reservation,
		action: (orgId: string, id: string) => Promise<Reservation>,
		success: string
	) {
		running = reservation.id;
		try {
			await action(orgId, reservation.id);
			toaster.success(success);
			await invalidateAll();
			// The list is re-read rather than patched in place. A local edit
			// would show a state the server may not have reached — and these
			// commands can be refused for reasons the browser cannot see, such
			// as somebody else having collected the equipment a moment ago.
			const reloaded = await listReservations(orgId, listQuery(listState));
			result = reloaded;
		} catch (error) {
			failure = error;
		} finally {
			running = null;
		}
	}

	function when(value: string): string {
		return new Date(value).toLocaleString();
	}

	const stateTone: Record<ReservationState, 'success' | 'warning' | 'neutral' | 'danger'> = {
		booked: 'neutral',
		checked_out: 'warning',
		returned: 'success',
		cancelled: 'neutral',
		no_show: 'danger'
	};
</script>

<svelte:head>
	<title>Reservations · Workbench</title>
</svelte:head>

<div class="mx-auto max-w-6xl px-4 py-8 sm:px-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight text-text">Reservations</h1>
			<p class="mt-1 text-sm text-text-muted">
				{#if result}
					{result.total}
					{result.total === 1 ? 'reservation' : 'reservations'}
				{/if}
			</p>
		</div>
		<Button variant="primary" href="/orgs/{orgId}/reservations/new">Book equipment</Button>
	</div>

	<!--
		The state filter is a list of links, not a select that navigates on
		change. Every filter is in the URL, so each one has a real href — which
		means it works without JavaScript, opens in a new tab, and is reachable
		by keyboard without a change handler firing on arrow keys.

		aria-current="page" is what tells a screen-reader user which filter is
		active. Colour alone would not.
	-->
	<nav class="mt-5 flex flex-wrap gap-2" aria-label="Filter by state">
		<a
			href={stateHref('')}
			aria-current={activeState === '' ? 'page' : undefined}
			class="rounded-full border border-line px-3 py-1 text-sm text-text hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary aria-[current]:border-primary aria-[current]:bg-primary/10 aria-[current]:font-medium aria-[current]:text-primary"
		>
			All
		</a>
		{#each reservationStates as value (value)}
			<a
				href={stateHref(value)}
				aria-current={activeState === value ? 'page' : undefined}
				class="rounded-full border border-line px-3 py-1 text-sm text-text hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary aria-[current]:border-primary aria-[current]:bg-primary/10 aria-[current]:font-medium aria-[current]:text-primary"
			>
				{reservationStateLabels[value]}
			</a>
		{/each}
		<a
			href={mineHref}
			aria-current={mine ? 'page' : undefined}
			class="ml-auto rounded-full border border-line px-3 py-1 text-sm text-text hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary aria-[current]:border-primary aria-[current]:bg-primary/10 aria-[current]:font-medium aria-[current]:text-primary"
		>
			Only mine
		</a>
	</nav>

	{#if failure}
		<div class="mt-6"><ProblemAlert error={failure} /></div>
	{:else if result === null}
		<div class="mt-6 space-y-2" aria-busy="true">
			<Skeleton class="h-12 w-full" />
			<Skeleton class="h-12 w-full" />
			<Skeleton class="h-12 w-full" />
		</div>
	{:else if result.items.length === 0}
		<div class="mt-6">
			<EmptyState
				title="Nothing here"
				description={activeState || mine
					? 'No reservations match this filter.'
					: 'Book a piece of equipment to see it here.'}
			>
				{#snippet action()}
					{#if activeState || mine}
						<Button href={listHref(page.url, { filters: { state: '', holder_id: '' } }, filters)}>
							Clear filters
						</Button>
					{:else}
						<Button variant="primary" href="/orgs/{orgId}/reservations/new">Book equipment</Button>
					{/if}
				{/snippet}
			</EmptyState>
		</div>
	{:else}
		<div class="mt-6">
			<Table caption="Reservations, earliest first">
				<thead class="border-b border-line bg-surface-muted text-left">
					<tr>
						<th scope="col" class="px-4 py-2 font-medium text-text-muted">Window</th>
						<th scope="col" class="hidden px-4 py-2 font-medium text-text-muted md:table-cell">
							Note
						</th>
						<th scope="col" class="px-4 py-2 font-medium text-text-muted">State</th>
						<th scope="col" class="px-4 py-2"><span class="sr-only">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each result.items as item (item.id)}
						<tr class="border-b border-line last:border-0">
							<td class="px-4 py-2">
								<!--
									A <time> element with a machine-readable datetime, so the
									displayed local string is not the only representation.
								-->
								<time datetime={item.starts_at} class="font-medium text-text">
									{when(item.starts_at)}
								</time>
								<span class="block text-sm text-text-muted">
									until <time datetime={item.ends_at}>{when(item.ends_at)}</time>
								</span>
								{#if item.note}
									<span class="mt-1 block text-sm text-text-muted md:hidden">{item.note}</span>
								{/if}
							</td>
							<td class="hidden max-w-xs truncate px-4 py-2 text-text-muted md:table-cell">
								{item.note}
							</td>
							<td class="px-4 py-2">
								<Badge tone={stateTone[item.state]}>
									{reservationStateLabels[item.state]}
								</Badge>
							</td>
							<td class="px-4 py-2">
								<div class="flex flex-wrap justify-end gap-2">
									{#if mayAct(item)}
										{#if item.state === 'booked'}
											<Button
												size="sm"
												disabled={running === item.id}
												onclick={() => run(item, checkOutReservation, 'Checked out.')}
											>
												<span aria-hidden="true">Check out</span>
												<span class="sr-only">
													Check out the reservation starting {when(item.starts_at)}
												</span>
											</Button>
											<Button
												size="sm"
												variant="ghost"
												disabled={running === item.id}
												onclick={() => run(item, cancelReservation, 'Cancelled.')}
											>
												<span aria-hidden="true">Cancel</span>
												<span class="sr-only">
													Cancel the reservation starting {when(item.starts_at)}
												</span>
											</Button>
										{:else if item.state === 'checked_out'}
											<Button
												size="sm"
												disabled={running === item.id}
												onclick={() => run(item, (o, id) => returnReservation(o, id), 'Returned.')}
											>
												<span aria-hidden="true">Return</span>
												<span class="sr-only">
													Return the reservation starting {when(item.starts_at)}
												</span>
											</Button>
										{/if}
									{/if}
								</div>
							</td>
						</tr>
					{/each}
				</tbody>
			</Table>
		</div>

		<Pagination
			label="Reservations"
			{nextHref}
			summary={`Showing ${result.items.length} of ${result.total}`}
		/>
	{/if}
</div>
