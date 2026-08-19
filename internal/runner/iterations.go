// How many iterations a collection run performs, and the ceiling on it.
//
// Here with the data file that decides the row count, rather than in the
// application package: a run's iteration count is the smaller of the requested
// number and the rows available, and the two halves of that rule were in
// different packages.
package runner

const IterationLimit = 200

func NormalizeIterations(iterations int) int {
	if iterations < 1 {
		return 1
	}
	if iterations > IterationLimit {
		return IterationLimit
	}
	return iterations
}
