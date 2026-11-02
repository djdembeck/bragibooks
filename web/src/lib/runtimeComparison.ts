export interface DurationReading {
	milliseconds: number;
	precision: 'exact' | 'minute-rounded';
}

export interface RuntimeEvidence {
	source: DurationReading;
	catalog: DurationReading;
}

export interface RuntimeComparisonResult {
	sourceLabel: string;
	catalogLabel: string;
	sourceRounded: boolean;
	catalogRounded: boolean;
	direction: 'source-longer' | 'catalog-longer' | 'equal' | 'uncertain';
	lowerMinutes: number;
	upperMinutes: number;
	exactWholeMinutes: boolean;
	underMinute: boolean;
	severity: 'neutral' | 'small' | 'noticeable' | 'moderate' | 'large' | 'very-large';
}

const MINUTE = 60_000;
const ROUNDING_HALF_MINUTE = 30_000;
const MAX_DURATION = Number.MAX_SAFE_INTEGER - MINUTE;

function validReading(reading: DurationReading | undefined): reading is DurationReading {
	if (!reading || !Number.isSafeInteger(reading.milliseconds) || reading.milliseconds <= 0 || reading.milliseconds > MAX_DURATION) {
		return false;
	}
	if (reading.precision === 'exact') return true;
	return reading.precision === 'minute-rounded' && reading.milliseconds % MINUTE === 0;
}

function durationLabel(milliseconds: number): string {
	const minutes = Math.round(milliseconds / MINUTE);
	if (minutes === 0) return 'Under 1 min';
	const hours = Math.floor(minutes / 60);
	const remainingMinutes = minutes % 60;
	if (hours === 0) return `${minutes}m`;
	if (remainingMinutes === 0) return `${hours}h`;
	return `${hours}h ${remainingMinutes}m`;
}

function severityFor(lowerMinutes: number): RuntimeComparisonResult['severity'] {
	if (lowerMinutes >= 60) return 'very-large';
	if (lowerMinutes >= 30) return 'large';
	if (lowerMinutes >= 10) return 'moderate';
	if (lowerMinutes >= 5) return 'noticeable';
	if (lowerMinutes >= 1) return 'small';
	return 'neutral';
}

/** Compare independently supplied runtime measurements without treating rounded displays as exact. */
export function compareRuntimeDurations(evidence: RuntimeEvidence | undefined): RuntimeComparisonResult | null {
	if (!evidence || !validReading(evidence.source) || !validReading(evidence.catalog)) return null;

	const { source, catalog } = evidence;
	const sourceRounded = source.precision === 'minute-rounded' || source.milliseconds % MINUTE !== 0;
	const catalogRounded = catalog.precision === 'minute-rounded' || catalog.milliseconds % MINUTE !== 0;
	const sourceLow = source.precision === 'exact' ? source.milliseconds : source.milliseconds - ROUNDING_HALF_MINUTE;
	const sourceHigh = source.precision === 'exact' ? source.milliseconds : source.milliseconds + ROUNDING_HALF_MINUTE;
	const catalogLow = catalog.precision === 'exact' ? catalog.milliseconds : catalog.milliseconds - ROUNDING_HALF_MINUTE;
	const catalogHigh = catalog.precision === 'exact' ? catalog.milliseconds : catalog.milliseconds + ROUNDING_HALF_MINUTE;
	const signedLow = sourceLow - catalogHigh;
	const signedHigh = sourceHigh - catalogLow;
	const hasRoundedInput = source.precision === 'minute-rounded' || catalog.precision === 'minute-rounded';
	const exactEqual = !hasRoundedInput && signedLow === 0;
	const uncertain = hasRoundedInput && signedLow <= 0 && signedHigh >= 0;

	let direction: RuntimeComparisonResult['direction'];
	let lowerBound: number;
	let upperBound: number;
	if (exactEqual) {
		direction = 'equal';
		lowerBound = 0;
		upperBound = 0;
	} else if (uncertain) {
		direction = 'uncertain';
		lowerBound = 0;
		upperBound = Math.max(Math.abs(signedLow), Math.abs(signedHigh));
	} else if (signedLow > 0) {
		direction = 'source-longer';
		lowerBound = signedLow;
		upperBound = signedHigh;
	} else {
		direction = 'catalog-longer';
		lowerBound = -signedHigh;
		upperBound = -signedLow;
	}

	const lowerMinutes = Math.floor(lowerBound / MINUTE);
	const upperMinutes = Math.ceil(upperBound / MINUTE);
	const bothExact = !hasRoundedInput;
	const difference = Math.abs(source.milliseconds - catalog.milliseconds);
	const exactWholeMinutes = bothExact && difference % MINUTE === 0;
	const underMinute = bothExact && difference > 0 && difference < MINUTE;
	const severity = direction === 'uncertain' || underMinute ? 'neutral' : severityFor(lowerMinutes);

	return {
		sourceLabel: durationLabel(source.milliseconds),
		catalogLabel: durationLabel(catalog.milliseconds),
		sourceRounded,
		catalogRounded,
		direction,
		lowerMinutes,
		upperMinutes,
		exactWholeMinutes,
		underMinute,
		severity
	};
}
