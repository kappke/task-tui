// Package repository defines consumer-facing persistence contracts.
//
// Implementations belong to storage packages. These interfaces deliberately
// stay independent of a particular database so application and synchronization
// code can depend on the smallest contract they need.
package repository
