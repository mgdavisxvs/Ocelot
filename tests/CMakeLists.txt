cmake_minimum_required(VERSION 3.14)

include_directories(
    ${CMAKE_SOURCE_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}
)

find_package(Boost REQUIRED COMPONENTS iostreams system)
find_package(Threads REQUIRED)

# ============================================================
# Source groups
# ============================================================

# Pure-function sources: no MySQL++, no Boost.Asio, no libev
set(PURE_SOURCES
    ${CMAKE_SOURCE_DIR}/misc_functions.cpp
    ${CMAKE_SOURCE_DIR}/config.cpp
)

# Response sources: need Boost.Iostreams (gzip)
set(RESPONSE_SOURCES
    ${CMAKE_SOURCE_DIR}/response.cpp
    ${CMAKE_SOURCE_DIR}/misc_functions.cpp
)

# Worker sources: db_interface + site_comm_interface only (no MySQL++, no Boost.Asio)
set(WORKER_SOURCES
    ${CMAKE_SOURCE_DIR}/worker.cpp
    ${CMAKE_SOURCE_DIR}/report.cpp
    ${CMAKE_SOURCE_DIR}/config.cpp
    ${CMAKE_SOURCE_DIR}/misc_functions.cpp
    ${CMAKE_SOURCE_DIR}/response.cpp
    ${CMAKE_SOURCE_DIR}/user.cpp
    ${CMAKE_CURRENT_SOURCE_DIR}/stats_stub.cpp
)

# ============================================================
# Helper macro
# ============================================================
macro(add_ocelot_test target)
    cmake_parse_arguments(ARG "" "" "SOURCES;LIBS" ${ARGN})
    add_executable(${target} ${ARG_SOURCES})
    target_include_directories(${target} PRIVATE
        ${CMAKE_SOURCE_DIR}
        ${CMAKE_CURRENT_SOURCE_DIR}
        ${Boost_INCLUDE_DIRS}
    )
    target_link_libraries(${target}
        GTest::gtest GTest::gtest_main
        ${ARG_LIBS}
        Threads::Threads
    )
    include(GoogleTest)
    gtest_discover_tests(${target})
endmacro()

# ============================================================
# Tier 1 — Unit tests (pure functions, no I/O)
# ============================================================

add_ocelot_test(test_misc_functions
    SOURCES
        test_misc_functions.cpp
        ${CMAKE_SOURCE_DIR}/misc_functions.cpp
)

add_ocelot_test(test_config
    SOURCES
        test_config.cpp
        ${PURE_SOURCES}
)

add_ocelot_test(test_response
    SOURCES
        test_response.cpp
        ${RESPONSE_SOURCES}
    LIBS
        Boost::iostreams
)

add_ocelot_test(test_user
    SOURCES
        test_user.cpp
        ${CMAKE_SOURCE_DIR}/user.cpp
        ${CMAKE_CURRENT_SOURCE_DIR}/stats_stub.cpp
)

# ============================================================
# Tier 2 — Module tests (mock I/O, real worker logic)
# ============================================================

add_ocelot_test(test_worker_announce
    SOURCES
        test_worker_announce.cpp
        ${WORKER_SOURCES}
    LIBS
        Boost::iostreams
        Boost::system
)

add_ocelot_test(test_worker_scrape
    SOURCES
        test_worker_scrape.cpp
        ${WORKER_SOURCES}
    LIBS
        Boost::iostreams
        Boost::system
)

add_ocelot_test(test_worker_update
    SOURCES
        test_worker_update.cpp
        ${WORKER_SOURCES}
    LIBS
        Boost::iostreams
        Boost::system
)

# ============================================================
# Tier 3 — Concurrency tests (compile with TSAN separately)
# ============================================================

add_executable(test_concurrency
    test_concurrency.cpp
    ${WORKER_SOURCES}
)
target_include_directories(test_concurrency PRIVATE
    ${CMAKE_SOURCE_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}
    ${Boost_INCLUDE_DIRS}
)
target_link_libraries(test_concurrency
    GTest::gtest GTest::gtest_main
    Boost::iostreams Boost::system
    Threads::Threads
)
# Apply thread-sanitizer flags to this target if ENABLE_TSAN is set
if(ENABLE_TSAN)
    target_compile_options(test_concurrency PRIVATE -fsanitize=thread -g -O1)
    target_link_options(test_concurrency PRIVATE -fsanitize=thread)
endif()
include(GoogleTest)
gtest_discover_tests(test_concurrency)

# ============================================================
# Tier 6 — Security tests
# ============================================================

add_ocelot_test(test_security
    SOURCES
        test_security.cpp
        ${WORKER_SOURCES}
    LIBS
        Boost::iostreams
        Boost::system
)

# Apply ASAN/UBSAN to security tests when requested
if(ENABLE_ASAN OR ENABLE_UBSAN)
    set(_san_flags "")
    if(ENABLE_ASAN)
        string(APPEND _san_flags " -fsanitize=address -fno-omit-frame-pointer")
    endif()
    if(ENABLE_UBSAN)
        string(APPEND _san_flags " -fsanitize=undefined")
    endif()
    separate_arguments(_san_list UNIX_COMMAND "${_san_flags}")
    target_compile_options(test_security PRIVATE ${_san_list})
    target_link_options(test_security PRIVATE ${_san_list})
endif()

# ============================================================
# Tier 7 — Benchmarks (standalone, no gtest dependency)
# ============================================================

add_executable(bench_announce
    bench_announce.cpp
    ${WORKER_SOURCES}
)
target_include_directories(bench_announce PRIVATE
    ${CMAKE_SOURCE_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}
    ${Boost_INCLUDE_DIRS}
)
target_compile_options(bench_announce PRIVATE -O3 -march=native -DNDEBUG)
target_link_libraries(bench_announce
    Boost::iostreams Boost::system Threads::Threads
)
