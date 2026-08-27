import type { Position } from './api'

export function selectActionRows(items: readonly Position[], limit?: number): Position[]
export function selectRecommendedRows(items: readonly Position[], limit?: number): Position[]
