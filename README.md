# EverestKV

EverestKV is a lightweight, Redis-inspired in-memory key-value store built in Go.  
It is designed as a systems programming project to understand how high-performance databases handle concurrency, networking, caching, and persistence under the hood.

The goal of this project is not to replace Redis, but to deeply learn and reimplement its core ideas in a simplified and educational way.

---

##  Why EverestKV?

Modern systems like Redis handle millions of requests per second using smart memory management, efficient concurrency models, and optimized networking.

EverestKV explores these concepts by building a minimal version from scratch using Go.

---

## Core Focus Areas

- In-memory data storage
- TCP-based custom protocol
- Concurrent request handling (goroutines)
- Thread-safe operations
- TTL (time-to-live) support *(planned)*
- Persistence using AOF / snapshot *(planned)*

---

##  Tech Stack

- Go (Golang)
- TCP Networking
- Concurrency (Goroutines & Mutex)
- File-based persistence (planned)

---

##  Project Goals

- Understand how Redis-like systems work internally
- Learn real-world backend + systems design concepts
- Build production-style architecture in a minimal system
- Create a strong open-source systems project for portfolio

---

## 🏔️ Philosophy

EverestKV is built as a learning-first project.  
It focuses on clarity, simplicity, and understanding systems fundamentals rather than feature completeness.

---

##  Status

🚧 Early development (core KV engine in progress)
