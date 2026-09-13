cmake_minimum_required(VERSION 3.14)
project(Ocelot VERSION 1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 14)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# ---- Options ---------------------------------------------------------------
option(BUILD_TESTS     "Build the Ocelot test suite"            OFF)
option(ENABLE_ASAN     "Enable AddressSanitizer on tests"       OFF)
option(ENABLE_TSAN     "Enable ThreadSanitizer on tests"        OFF)
option(ENABLE_UBSAN    "Enable UBSanitizer on tests"            OFF)

# ---- Find dependencies (needed even if tests-only) -------------------------
find_package(Boost REQUIRED COMPONENTS iostreams system)

# ---- Sanitizer flags -------------------------------------------------------
set(SANITIZER_FLAGS "")
if(ENABLE_ASAN)
    string(APPEND SANITIZER_FLAGS " -fsanitize=address -fno-omit-frame-pointer")
endif()
if(ENABLE_TSAN)
    string(APPEND SANITIZER_FLAGS " -fsanitize=thread")
endif()
if(ENABLE_UBSAN)
    string(APPEND SANITIZER_FLAGS " -fsanitize=undefined")
endif()
if(SANITIZER_FLAGS)
    set(CMAKE_CXX_FLAGS_DEBUG "${CMAKE_CXX_FLAGS_DEBUG} ${SANITIZER_FLAGS}")
    set(CMAKE_EXE_LINKER_FLAGS_DEBUG "${CMAKE_EXE_LINKER_FLAGS_DEBUG} ${SANITIZER_FLAGS}")
endif()

# ---- Tests -----------------------------------------------------------------
if(BUILD_TESTS)
    find_package(GTest REQUIRED)

    enable_testing()
    add_subdirectory(tests)
endif()
