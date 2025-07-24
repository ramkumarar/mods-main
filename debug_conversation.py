#!/usr/bin/env python3
"""
Debug script to check what's in a conversation file
"""
import sqlite3
import os
from pathlib import Path

def debug_conversation(conversation_id):
    """Debug a specific conversation"""
    
    # Find the cache directory
    cache_dir = Path.home() / "AppData" / "Local" / "mods"
    if not cache_dir.exists():
        print(f"Cache directory not found: {cache_dir}")
        return
    
    # Check database
    db_path = cache_dir / "conversations" / "mods.db"
    if not db_path.exists():
        print(f"Database not found: {db_path}")
        return
    
    print(f"Checking database: {db_path}")
    
    # Connect to database
    conn = sqlite3.connect(str(db_path))
    cursor = conn.cursor()
    
    # Check if conversation exists in database
    cursor.execute("SELECT id, title, updated_at, api, model FROM conversations WHERE id LIKE ?", (f"{conversation_id}%",))
    rows = cursor.fetchall()
    
    if not rows:
        print(f"No conversation found with ID starting with: {conversation_id}")
        conn.close()
        return
    
    for row in rows:
        print(f"Database entry:")
        print(f"  ID: {row[0]}")
        print(f"  Title: {row[1]}")
        print(f"  Updated: {row[2]}")
        print(f"  API: {row[3]}")
        print(f"  Model: {row[4]}")
        
        # Check if conversation file exists
        conv_file = cache_dir / "conversations" / f"{row[0]}.gob"
        if conv_file.exists():
            print(f"  File exists: {conv_file}")
            print(f"  File size: {conv_file.stat().st_size} bytes")
        else:
            print(f"  File missing: {conv_file}")
    
    conn.close()

if __name__ == "__main__":
    # Use the conversation ID you showed
    conversation_id = "428edf1fc294c3db0cb3d06bdf04a616e987ee72"
    debug_conversation(conversation_id)