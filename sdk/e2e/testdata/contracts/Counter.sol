// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract Counter {
    uint256 public value;

    function inc() public returns (uint256) {
        value += 1;
        return value;
    }
}
